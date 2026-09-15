package merch_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test/fixtures"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func checkout(t *testing.T, s *api.Server, email string, lines []map[string]any) *httptest.ResponseRecorder {
	t.Helper()

	return test.PerformRequest(t, s, "POST", "/api/v1/checkout", test.GenericPayload{
		"lines":         lines,
		"email":         email,
		"fullName":      "Test Buyer",
		"shippingState": "Lagos",
	}, nil)
}

func line(variantID string, quantity int) map[string]any {
	return map[string]any{"variantId": variantID, "quantity": quantity}
}

func TestProductsListPublishedOnlyWithAvailability(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/products", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var products types.ProductList
		test.ParseResponseAndValidate(t, res, &products)

		require.Len(t, products.Items, 1, "the unpublished product must not appear")
		assert.Equal(t, "cloudadore-hoodie", *products.Items[0].Slug)
		assert.Len(t, products.Items[0].Variants, 3)

		for _, variant := range products.Items[0].Variants {
			assert.GreaterOrEqual(t, *variant.Available, int64(0), "availability is never negative")
		}
	})
}

// Totals are computed server-side. The response must reflect the shipping zone
// and the configured tax rate, not anything the client sent.
func TestCheckoutComputesTotalsServerSide(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		res := checkout(t, s, "buyer@example.com", []map[string]any{line(fix.VariantHoodieM.ID, 1)})
		require.Equal(t, http.StatusCreated, res.Result().StatusCode)

		var result types.CheckoutResponse
		test.ParseResponseAndValidate(t, res, &result)

		assert.Equal(t, int64(1500000), *result.SubtotalKobo)
		assert.Equal(t, int64(250000), *result.ShippingKobo, "the flat nationwide rate applies")
		assert.Equal(t, int64(0), *result.TaxKobo, "no VAT rate is configured by default")
		assert.Equal(t, *result.SubtotalKobo+*result.ShippingKobo+*result.TaxKobo, *result.TotalKobo)
		assert.NotEmpty(t, *result.AccessToken)
	})
}

// The free-shipping threshold is part of the zone, so it must apply without
// any client involvement.
func TestCheckoutAppliesFreeShippingThreshold(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		// 3 hoodies is ₦45,000, above the ₦50,000 threshold? No -- 3 x 15,000 =
		// 45,000, below it. Two variants at 3 each clears it.
		res := checkout(t, s, "bulk@example.com", []map[string]any{
			line(fix.VariantHoodieM.ID, 3),
			line(fix.VariantHoodieL.ID, 1),
		})
		require.Equal(t, http.StatusCreated, res.Result().StatusCode)

		var result types.CheckoutResponse
		test.ParseResponseAndValidate(t, res, &result)

		assert.Equal(t, int64(6000000), *result.SubtotalKobo)
		assert.Equal(t, int64(0), *result.ShippingKobo, "orders above the threshold ship free")
	})
}

// A sold-out line must name itself, so the client can mark exactly which item
// to fix rather than failing the whole basket opaquely.
func TestCheckoutConflictNamesTheFailedLines(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		res := checkout(t, s, "unlucky@example.com", []map[string]any{
			line(fix.VariantHoodieM.ID, 1),
			line(fix.VariantSoldOut.ID, 1),
		})
		require.Equal(t, http.StatusConflict, res.Result().StatusCode)

		var conflict types.CheckoutConflictResponse
		test.ParseResponseAndValidate(t, res, &conflict)

		require.Len(t, conflict.Problems, 1, "only the sold-out line is a problem")
		assert.Equal(t, fix.VariantSoldOut.ID, conflict.Problems[0].VariantID.String())
		assert.Equal(t, "out_of_stock", *conflict.Problems[0].Reason)
		assert.Equal(t, int64(0), *conflict.Problems[0].Available)
	})
}

// A variant belonging to an unpublished product must not be buyable, even with
// a valid id.
func TestCheckoutRefusesUnpublishedProduct(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		res := checkout(t, s, "sneaky@example.com", []map[string]any{line(fix.VariantDraft.ID, 1)})
		require.Equal(t, http.StatusConflict, res.Result().StatusCode)

		var conflict types.CheckoutConflictResponse
		test.ParseResponseAndValidate(t, res, &conflict)

		require.Len(t, conflict.Problems, 1)
		assert.Equal(t, "unavailable", *conflict.Problems[0].Reason)
	})
}

// Nothing is reserved when the cart is rejected: a failed checkout must not
// quietly hold stock.
func TestFailedCheckoutReservesNothing(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		res := checkout(t, s, "nope@example.com", []map[string]any{
			line(fix.VariantHoodieM.ID, 1),
			line(fix.VariantSoldOut.ID, 1),
		})
		require.Equal(t, http.StatusConflict, res.Result().StatusCode)

		variant, err := models.FindProductVariant(t.Context(), s.DB, fix.VariantHoodieM.ID)
		require.NoError(t, err)
		assert.Equal(t, 0, variant.StockReserved, "the good line must not stay reserved")
	})
}

func TestEmptyCartIsRejected(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "POST", "/api/v1/checkout", test.GenericPayload{
			"lines": []map[string]any{}, "email": "x@example.com",
			"fullName": "X", "shippingState": "Lagos",
		}, nil)
		assert.Equal(t, http.StatusBadRequest, res.Result().StatusCode)
	})
}

// The cart is bounded, and the bound is a denial-of-service control rather than
// a usability one.
//
// Checkout holds one pooled connection for the whole transaction and takes a row
// lock per line. Unbounded, an anonymous caller could send thousands of lines in
// a single request and occupy the pool -- which is ten connections -- for as long
// as it takes to walk them. The 1MB body limit alone still permits some 17,000
// lines, so the bound has to be on the array.
func TestOversizedCartIsRejected(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		variantID := fixtures.Fixtures().VariantHoodieM.ID

		lines := make([]map[string]any, 0, 51)
		for range 51 {
			lines = append(lines, map[string]any{"variantId": variantID, "quantity": 1})
		}

		res := test.PerformRequest(t, s, "POST", "/api/v1/checkout", test.GenericPayload{
			"lines": lines, "email": "x@example.com",
			"fullName": "X", "shippingState": "Lagos",
		}, nil)

		assert.Equal(t, http.StatusBadRequest, res.Result().StatusCode,
			"a cart past the bound must be refused before any row is locked")
	})
}

// The stock twin of the ticketing race. Same single-statement reservation, so
// the same property must hold.
func TestConcurrentCheckoutsNeverOversell(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		const attempts = 10

		var (
			wg      sync.WaitGroup
			mutex   sync.Mutex
			created int
			other   []int
		)

		wg.Add(attempts)

		for i := range attempts {
			go func(n int) {
				defer wg.Done()

				res := checkout(t, s, fmt.Sprintf("racer-%d@example.com", n),
					[]map[string]any{line(fix.VariantHoodieM.ID, 1)})

				mutex.Lock()
				defer mutex.Unlock()

				switch res.Result().StatusCode {
				case http.StatusCreated:
					created++
				case http.StatusConflict:
				default:
					other = append(other, res.Result().StatusCode)
				}
			}(i)
		}

		wg.Wait()

		assert.Empty(t, other, "every attempt must be a clean 201 or 409, got %v", other)
		assert.Equal(t, fix.VariantHoodieM.StockTotal, created, "exactly the available stock may sell")

		variant, err := models.FindProductVariant(t.Context(), s.DB, fix.VariantHoodieM.ID)
		require.NoError(t, err)
		assert.LessOrEqual(t, variant.StockReserved+variant.StockSold, variant.StockTotal)
	})
}

// Two carts holding the SAME two variants in OPPOSITE orders. Without a
// deterministic lock order these deadlock against each other inside their
// transactions; sorting by variant id makes that unreachable.
//
// A single-line load test cannot detect this, which is why the cart here has
// two lines.
func TestOppositeOrderCartsDoNotDeadlock(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		const pairs = 6

		var (
			wg       sync.WaitGroup
			mutex    sync.Mutex
			statuses []int
		)

		wg.Add(pairs * 2)

		record := func(res *httptest.ResponseRecorder) {
			mutex.Lock()
			defer mutex.Unlock()
			statuses = append(statuses, res.Result().StatusCode)
		}

		for i := range pairs {
			go func(n int) {
				defer wg.Done()
				record(checkout(t, s, fmt.Sprintf("fwd-%d@example.com", n), []map[string]any{
					line(fix.VariantHoodieM.ID, 1),
					line(fix.VariantHoodieL.ID, 1),
				}))
			}(i)

			go func(n int) {
				defer wg.Done()
				record(checkout(t, s, fmt.Sprintf("rev-%d@example.com", n), []map[string]any{
					line(fix.VariantHoodieL.ID, 1),
					line(fix.VariantHoodieM.ID, 1),
				}))
			}(i)
		}

		wg.Wait()

		for _, status := range statuses {
			assert.Contains(t, []int{http.StatusCreated, http.StatusConflict}, status,
				"a deadlock would surface as a 500 here, got %d", status)
		}
	})
}
