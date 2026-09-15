package local

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/httperrors"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/db"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/hashing"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/money"
)

// reserveStock is the merch twin of reserveSeats, and identical on purpose.
//
// Same single statement, same reason: the predicate and the increment must not
// be separable, or two concurrent carts both read "1 left" and both take it.
func reserveStock(ctx context.Context, exec boil.ContextExecutor, variantID string, quantity int) error {
	result, err := queries.Raw(
		`UPDATE product_variants
		    SET stock_reserved = stock_reserved + $2,
		        updated_at = now()
		  WHERE id = $1
		    AND stock_reserved + stock_sold + $2 <= stock_total`,
		variantID, quantity,
	).ExecContext(ctx, exec)
	if err != nil {
		return fmt.Errorf("failed to reserve stock: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}

	if affected != 1 {
		return httperrors.ErrConflictInsufficientStock
	}

	return nil
}

// ListProducts returns the published catalogue.
func (s *Service) ListProducts(ctx context.Context) (dto.ProductList, error) {
	products, err := models.Products(
		models.ProductWhere.PublishedAt.IsNotNull(),
		models.ProductWhere.ArchivedAt.IsNull(),
		qm.Load(models.ProductRels.ProductVariants, models.ProductVariantWhere.ArchivedAt.IsNull()),
		qm.Load(models.ProductRels.HeroMedium),
		qm.OrderBy(models.ProductColumns.PublishedAt+" DESC"),
	).All(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("failed to list products: %w", err)
	}

	result := make(dto.ProductList, len(products))

	for i, product := range products {
		item := dto.Product{
			ID:          product.ID,
			Slug:        product.Slug,
			Title:       product.Title,
			Description: product.Description,
		}

		if product.R != nil && product.R.HeroMedium != nil {
			item.ImageURL = product.R.HeroMedium.PublicURL
		}

		if product.R != nil {
			for _, variant := range product.R.ProductVariants {
				available := variant.StockTotal - variant.StockSold - variant.StockReserved
				if available < 0 {
					available = 0
				}

				item.Variants = append(item.Variants, dto.ProductVariant{
					ID:        variant.ID,
					SKU:       variant.Sku,
					Size:      variant.Size,
					Colour:    variant.Colour,
					PriceKobo: variant.PriceKobo,
					Available: available,
				})
			}
		}

		result[i] = item
	}

	return result, nil
}

// Checkout turns a client-side cart into an order and a payment intent.
//
// The cart lives in the browser, so everything in the request is a claim. Only
// the variant ids and quantities are honoured: prices, totals and shipping are
// all looked up or computed here. A client that sends a price is ignored, not
// rejected, because rejecting would tell an attacker which field mattered.
func (s *Service) Checkout(ctx context.Context, request dto.CheckoutRequest) (dto.CheckoutResult, error) {
	log := util.LogFromContext(ctx)

	if len(request.Lines) == 0 {
		return dto.CheckoutResult{}, httperrors.ErrBadRequestEmptyCart
	}

	var result dto.CheckoutResult

	err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		variants, problems, err := s.resolveCartLines(ctx, exec, request.Lines)
		if err != nil {
			return err
		}

		if len(problems) > 0 {
			// A structured 409 naming each failed line, not a bare error. The cart
			// is client-side, so the frontend has to be able to say WHICH item is
			// gone -- otherwise every out-of-stock size shows "something went
			// wrong".
			result.Problems = problems

			return httperrors.ErrConflictCartChanged
		}

		subtotal := int64(0)
		for _, line := range variants {
			lineTotal, err := money.LineTotal(line.variant.PriceKobo, line.quantity)
			if err != nil {
				return fmt.Errorf("failed to compute line total: %w", err)
			}

			subtotal += lineTotal
		}

		shippingKobo, err := s.shippingFor(ctx, exec, request.ShippingState, subtotal)
		if err != nil {
			return err
		}

		totals, err := money.ComputeTotals(subtotal, shippingKobo, 0, money.BasisPoints(s.config.Payment.TaxRateBasisPoints))
		if err != nil {
			return fmt.Errorf("failed to compute totals: %w", err)
		}

		reference, err := newPaymentReference("mch")
		if err != nil {
			return err
		}

		accessTokenPlain, accessTokenHash, err := hashing.NewToken()
		if err != nil {
			return fmt.Errorf("failed to generate order access token: %w", err)
		}

		order := &models.Order{
			Reference:            reference,
			Kind:                 models.OrderKindMerch,
			Status:               models.OrderStatusPending,
			UserID:               request.UserID,
			Email:                request.Email,
			FullName:             request.FullName,
			Phone:                request.Phone,
			ShippingLine1:        request.ShippingLine1,
			ShippingLine2:        request.ShippingLine2,
			ShippingCity:         request.ShippingCity,
			ShippingState:        null.StringFrom(request.ShippingState),
			ShippingCountry:      request.ShippingCountry,
			ShippingPostcode:     request.ShippingPostcode,
			SubtotalKobo:         totals.SubtotalKobo,
			ShippingKobo:         totals.ShippingKobo,
			TaxKobo:              totals.TaxKobo,
			DiscountKobo:         totals.DiscountKobo,
			TotalKobo:            totals.TotalKobo,
			ReservationExpiresAt: null.TimeFrom(s.clock.Now().Add(s.config.Payment.IntentValidity)),
			AccessTokenHash:      null.BytesFrom(accessTokenHash),
		}

		if err := order.Insert(ctx, exec, boil.Infer()); err != nil {
			return fmt.Errorf("failed to insert order: %w", err)
		}

		// Reserve in a deterministic order. Two carts holding the same two
		// variants in opposite orders would otherwise deadlock inside one
		// transaction; sorting by id gives every transaction the same acquisition
		// order, which makes that unreachable.
		for _, line := range variants {
			if err := reserveStock(ctx, exec, line.variant.ID, line.quantity); err != nil {
				return err
			}

			lineTotal, err := money.LineTotal(line.variant.PriceKobo, line.quantity)
			if err != nil {
				return fmt.Errorf("failed to compute line total: %w", err)
			}

			item := &models.OrderItem{
				OrderID:          order.ID,
				ProductVariantID: null.StringFrom(line.variant.ID),
				TitleSnapshot:    line.title,
				VariantSnapshot:  variantLabel(line.variant),
				UnitPriceKobo:    line.variant.PriceKobo,
				Quantity:         line.quantity,
				LineTotalKobo:    lineTotal,
			}

			if err := item.Insert(ctx, exec, boil.Infer()); err != nil {
				return fmt.Errorf("failed to insert order item: %w", err)
			}
		}

		paymentRow := &models.Payment{
			Provider:   s.paymentProviderName,
			Status:     models.PaymentStatusPending,
			OrderID:    null.StringFrom(order.ID),
			Reference:  reference,
			AmountKobo: totals.TotalKobo,
			ExpiresAt:  s.clock.Now().Add(s.config.Payment.IntentValidity),
		}

		if err := paymentRow.Insert(ctx, exec, boil.Infer()); err != nil {
			return fmt.Errorf("failed to insert payment: %w", err)
		}

		result = dto.CheckoutResult{
			Reference:   reference,
			AccessToken: accessTokenPlain,
			Totals:      totals,
		}

		return nil
	})
	if err != nil {
		return result, err
	}

	initialized, err := s.payment.InitializeTransaction(ctx, payment.InitializeRequest{
		Reference:   result.Reference,
		AmountMinor: result.Totals.TotalKobo,
		Currency:    models.CurrencyCodeNGN,
		Email:       request.Email,
		CallbackURL: s.config.Payment.CallbackURL,
	})
	if err != nil {
		log.Err(err).Str("reference", result.Reference).Msg("Failed to initialize merch payment")

		return dto.CheckoutResult{}, httperrors.ErrBadGatewayPaymentProvider
	}

	if err := s.recordAuthorizationURL(ctx, result.Reference, initialized.AuthorizationURL); err != nil {
		return dto.CheckoutResult{}, err
	}

	result.AuthorizationURL = initialized.AuthorizationURL

	return result, nil
}

type cartLine struct {
	variant  *models.ProductVariant
	quantity int
	title    string
}

// resolveCartLines looks up every variant and reports what is wrong, rather than
// failing on the first problem -- so the client can fix a whole cart in one go.
func (s *Service) resolveCartLines(ctx context.Context, exec boil.ContextExecutor, lines []dto.CartLine) ([]cartLine, []dto.CartProblem, error) {
	resolved := make([]cartLine, 0, len(lines))
	problems := make([]dto.CartProblem, 0)

	for _, line := range lines {
		variant, err := models.ProductVariants(
			models.ProductVariantWhere.ID.EQ(line.VariantID),
			models.ProductVariantWhere.ArchivedAt.IsNull(),
			qm.Load(models.ProductVariantRels.Product),
		).One(ctx, exec)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				problems = append(problems, dto.CartProblem{
					VariantID: line.VariantID,
					Reason:    dto.CartProblemUnavailable,
				})

				continue
			}

			return nil, nil, fmt.Errorf("failed to load variant: %w", err)
		}

		if variant.R == nil || variant.R.Product == nil ||
			!variant.R.Product.PublishedAt.Valid || variant.R.Product.ArchivedAt.Valid {
			problems = append(problems, dto.CartProblem{
				VariantID: line.VariantID,
				Reason:    dto.CartProblemUnavailable,
			})

			continue
		}

		available := variant.StockTotal - variant.StockSold - variant.StockReserved
		if available < line.Quantity {
			problems = append(problems, dto.CartProblem{
				VariantID: line.VariantID,
				Reason:    dto.CartProblemOutOfStock,
				Available: max(available, 0),
			})

			continue
		}

		resolved = append(resolved, cartLine{
			variant:  variant,
			quantity: line.Quantity,
			title:    variant.R.Product.Title,
		})
	}

	// Deterministic lock order, by variant id. See the note at the reservation
	// loop: without this, two carts sharing variants in opposite orders deadlock.
	sortCartLines(resolved)

	return resolved, problems, nil
}

func sortCartLines(lines []cartLine) {
	for i := 1; i < len(lines); i++ {
		for j := i; j > 0 && lines[j].variant.ID < lines[j-1].variant.ID; j-- {
			lines[j], lines[j-1] = lines[j-1], lines[j]
		}
	}
}

func variantLabel(variant *models.ProductVariant) string {
	// A label is at most "size / colour".
	const maxLabelParts = 2

	parts := make([]string, 0, maxLabelParts)
	if variant.Size.Valid && variant.Size.String != "" {
		parts = append(parts, variant.Size.String)
	}

	if variant.Colour.Valid && variant.Colour.String != "" {
		parts = append(parts, variant.Colour.String)
	}

	return strings.Join(parts, " / ")
}

// shippingFor picks the first matching zone by sort order.
//
// A zone with no states matches everything, which is how a single flat
// nationwide row works. A more specific zone must sort ahead of it.
func (s *Service) shippingFor(ctx context.Context, exec boil.ContextExecutor, state string, subtotalKobo int64) (int64, error) {
	zones, err := models.ShippingZones(
		models.ShippingZoneWhere.ArchivedAt.IsNull(),
		qm.OrderBy(models.ShippingZoneColumns.SortOrder),
	).All(ctx, exec)
	if err != nil {
		return 0, fmt.Errorf("failed to load shipping zones: %w", err)
	}

	normalized := strings.ToLower(strings.TrimSpace(state))

	for _, zone := range zones {
		if !zoneMatches(zone, normalized) {
			continue
		}

		if zone.FreeAboveKobo > 0 && subtotalKobo >= zone.FreeAboveKobo {
			return 0, nil
		}

		return zone.RateKobo, nil
	}

	// No zone configured at all means no charge, not a failed checkout: the
	// alternative is a shop that cannot sell until someone remembers to add a
	// rate row.
	return 0, nil
}

func zoneMatches(zone *models.ShippingZone, normalizedState string) bool {
	if len(zone.States) == 0 {
		return true
	}

	for _, candidate := range zone.States {
		if strings.EqualFold(strings.TrimSpace(candidate), normalizedState) {
			return true
		}
	}

	return false
}
