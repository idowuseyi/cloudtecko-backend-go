package dto

import (
	"net/http"
	"time"

	"github.com/aarondl/null/v8"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/swag/conv"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/money"
)

// GalleryStatus is the availability badge an event card carries.
//
// It is DERIVED from ticket-tier capacity every time it is read, never stored.
// A stored copy would go stale on every ticket sale, and a column would have to
// be nullable for resources, which do not have the concept at all.
type GalleryStatus string

const (
	GalleryStatusOpen    GalleryStatus = "Open"
	GalleryStatusLimited GalleryStatus = "Limited"
	GalleryStatusSoldOut GalleryStatus = "Sold Out"
)

// GalleryLimitedThreshold is the fraction of capacity remaining at or below which
// an event reads as "Limited" rather than "Open".
const GalleryLimitedThreshold = 0.1

// DeriveGalleryStatus maps remaining capacity onto the badge. A tier total of 0
// means the event does not sell tickets (a free, uncapped RSVP), which is always
// Open rather than instantly Sold Out.
func DeriveGalleryStatus(sold int, capacity int) null.String {
	if capacity <= 0 {
		return null.StringFrom(string(GalleryStatusOpen))
	}

	remaining := capacity - sold
	switch {
	case remaining <= 0:
		return null.StringFrom(string(GalleryStatusSoldOut))
	case float64(remaining) <= float64(capacity)*GalleryLimitedThreshold:
		return null.StringFrom(string(GalleryStatusLimited))
	default:
		return null.StringFrom(string(GalleryStatusOpen))
	}
}

// GalleryItem is the read projection that both events and resources render into.
//
// It is deliberately NOT a storage shape -- nothing persists a GalleryItem. It
// exists because the frontend feeds both content types through one component
// using one `ResourceItem` type, and that component reuse is a frontend fact
// that should not reach back and dictate how the two are stored.
type GalleryItem struct {
	ID          string
	Category    string
	Date        time.Time
	Title       string
	Description string
	ImageURL    string
	Href        null.String
	// Status is populated for events only; nil for resources.
	Status null.String
}

func (g GalleryItem) ToTypes() *types.GalleryItem {
	return &types.GalleryItem{
		ID:          conv.Pointer(g.ID),
		Category:    conv.Pointer(g.Category),
		Date:        conv.Pointer(strfmt.DateTime(g.Date)),
		Title:       conv.Pointer(g.Title),
		Description: conv.Pointer(g.Description),
		Image:       conv.Pointer(g.ImageURL),
		Href:        g.Href.Ptr(),
		Status:      g.Status.Ptr(),
	}
}

// GalleryPage is one page of gallery items plus the tab list rendered above them.
type GalleryPage struct {
	Items      []GalleryItem
	Categories []string
	Total      int64
	Page       int64
	PerPage    int64
}

func (p GalleryPage) ToTypes() *types.GalleryResponse {
	items := make([]*types.GalleryItem, len(p.Items))
	for i := range p.Items {
		items[i] = p.Items[i].ToTypes()
	}

	categories := p.Categories
	if categories == nil {
		categories = []string{}
	}

	return &types.GalleryResponse{
		Items:      items,
		Categories: categories,
		Total:      conv.Pointer(p.Total),
		Page:       conv.Pointer(p.Page),
		PerPage:    conv.Pointer(p.PerPage),
	}
}

// EventDetail is a single event, including the wall-clock intent alongside the
// absolute instants so a client can render "9AM - 5PM WAT" for a Lagos event
// regardless of where the viewer is.
type EventDetail struct {
	ID           string
	Slug         string
	Title        string
	Summary      string
	Body         string
	Category     string
	ImageURL     null.String
	StartAt      time.Time
	EndAt        time.Time
	Timezone     string
	IsOnline     bool
	VenueName    null.String
	VenueAddress null.String
	MeetingURL   null.String
	IsFeatured   bool
	Status       null.String
}

func (e EventDetail) ToTypes() *types.EventDetail {
	return &types.EventDetail{
		ID:           conv.Pointer(strfmt.UUID4(e.ID)),
		Slug:         conv.Pointer(e.Slug),
		Title:        conv.Pointer(e.Title),
		Summary:      conv.Pointer(e.Summary),
		Body:         conv.Pointer(e.Body),
		Category:     conv.Pointer(e.Category),
		Image:        e.ImageURL.Ptr(),
		StartAt:      conv.Pointer(strfmt.DateTime(e.StartAt)),
		EndAt:        conv.Pointer(strfmt.DateTime(e.EndAt)),
		Timezone:     conv.Pointer(e.Timezone),
		IsOnline:     conv.Pointer(e.IsOnline),
		VenueName:    e.VenueName.Ptr(),
		VenueAddress: e.VenueAddress.Ptr(),
		MeetingURL:   e.MeetingURL.Ptr(),
		IsFeatured:   conv.Pointer(e.IsFeatured),
		Status:       e.Status.Ptr(),
	}
}

// ResourceDetail is a single learning resource.
type ResourceDetail struct {
	ID                 string
	Slug               string
	Title              string
	Summary            string
	Body               string
	Kind               string
	Category           string
	ImageURL           null.String
	Author             string
	ContentURL         null.String
	ReadingTimeMinutes int
	PublishedAt        null.Time
}

func (r ResourceDetail) ToTypes() *types.ResourceDetail {
	result := &types.ResourceDetail{
		ID:                 conv.Pointer(strfmt.UUID4(r.ID)),
		Slug:               conv.Pointer(r.Slug),
		Title:              conv.Pointer(r.Title),
		Summary:            conv.Pointer(r.Summary),
		Body:               conv.Pointer(r.Body),
		Kind:               conv.Pointer(r.Kind),
		Category:           conv.Pointer(r.Category),
		Image:              r.ImageURL.Ptr(),
		Author:             conv.Pointer(r.Author),
		ContentURL:         r.ContentURL.Ptr(),
		ReadingTimeMinutes: conv.Pointer(int64(r.ReadingTimeMinutes)),
	}

	if r.PublishedAt.Valid {
		result.PublishedAt = conv.Pointer(strfmt.DateTime(r.PublishedAt.Time))
	}

	return result
}

// ContentListRequest is the shared query shape for both gallery endpoints.
type ContentListRequest struct {
	Search       null.String
	CategorySlug null.String
	Page         int64
	PerPage      int64
}

// Offset converts the 1-based page number into a SQL OFFSET.
func (r ContentListRequest) Offset() int {
	if r.Page < 1 {
		return 0
	}

	return int((r.Page - 1) * r.PerPage)
}

// --- Admin write shapes -----------------------------------------------------

// EventWriteRequest is the admin create/update payload for an event.
// Slug is absent by design: it is derived from Title on create and is immutable
// afterwards, because changing a published slug breaks every shared link and
// every search result already pointing at it.
type EventWriteRequest struct {
	Title        string
	Summary      string
	Body         string
	CategorySlug string
	HeroMediaID  null.String
	StartAt      time.Time
	EndAt        time.Time
	Timezone     string
	IsOnline     bool
	VenueName    null.String
	VenueAddress null.String
	MeetingURL   null.String
}

// ResourceWriteRequest is the admin create/update payload for a resource.
type ResourceWriteRequest struct {
	Title              string
	Summary            string
	Body               string
	Kind               string
	CategorySlug       string
	HeroMediaID        null.String
	Author             string
	ContentURL         null.String
	ReadingTimeMinutes int
}

// CategoryWriteRequest is the admin create/update payload for a category.
type CategoryWriteRequest struct {
	Scope     string
	Slug      string
	Name      string
	SortOrder int
}

// Category is a taxonomy entry as returned to admins.
type Category struct {
	ID        string
	Scope     string
	Slug      string
	Name      string
	SortOrder int
}

func (c Category) ToTypes() *types.Category {
	return &types.Category{
		ID:        conv.Pointer(strfmt.UUID4(c.ID)),
		Scope:     conv.Pointer(c.Scope),
		Slug:      conv.Pointer(c.Slug),
		Name:      conv.Pointer(c.Name),
		SortOrder: conv.Pointer(int64(c.SortOrder)),
	}
}

// CategoryList wraps categories for the admin listing response.
type CategoryList []Category

func (l CategoryList) ToTypes() *types.CategoryList {
	items := make([]*types.Category, len(l))
	for i := range l {
		items[i] = l[i].ToTypes()
	}

	return &types.CategoryList{Items: items}
}

// --- Media ------------------------------------------------------------------

// MediaPresignRequest is what an admin declares BEFORE uploading. Every field is
// a claim: a presigned PUT cannot constrain the bytes the client actually sends,
// so these are used for an early courtesy rejection and for reporting promised
// versus actual at completion -- never as the security control.
type MediaPresignRequest struct {
	Filename         string
	ContentType      string
	ByteSize         int64
	UploadedByUserID string
}

// MediaPresign is the upload capability handed back to the client.
type MediaPresign struct {
	ID        string
	UploadURL string
	ObjectKey string
	ExpiresAt time.Time
}

func (m MediaPresign) ToTypes() *types.MediaPresignResponse {
	return &types.MediaPresignResponse{
		ID:        conv.Pointer(strfmt.UUID4(m.ID)),
		UploadURL: conv.Pointer(m.UploadURL),
		ExpiresAt: conv.Pointer(strfmt.DateTime(m.ExpiresAt)),
	}
}

// Media is a stored asset as reported after verification.
type Media struct {
	ID          string
	Status      string
	ContentType string
	ByteSize    int64
	PublicURL   null.String
}

func (m Media) ToTypes() *types.Media {
	return &types.Media{
		ID:          conv.Pointer(strfmt.UUID4(m.ID)),
		Status:      conv.Pointer(m.Status),
		ContentType: conv.Pointer(m.ContentType),
		ByteSize:    conv.Pointer(m.ByteSize),
		PublicURL:   m.PublicURL.Ptr(),
	}
}

// --- Community --------------------------------------------------------------

type ContactMessageRequest struct {
	Name      string
	Email     string
	Subject   string
	Message   string
	SourceIP  string
	UserAgent string
}

type FAQ struct {
	ID       string
	Question string
	Answer   string
	Category string
}

type FAQList []FAQ

func (l FAQList) ToTypes() *types.FAQList {
	items := make([]*types.FAQ, len(l))
	for i := range l {
		items[i] = &types.FAQ{
			ID:       conv.Pointer(strfmt.UUID4(l[i].ID)),
			Question: conv.Pointer(l[i].Question),
			Answer:   conv.Pointer(l[i].Answer),
			Category: conv.Pointer(l[i].Category),
		}
	}

	return &types.FAQList{Items: items}
}

type MembershipApplicationRequest struct {
	Email        string
	FullName     string
	Motivation   string
	Organisation string
}

type MembershipApplication struct {
	ID           string
	Email        string
	FullName     string
	Motivation   string
	Organisation null.String
	Status       string
	ReviewNote   null.String
	CreatedAt    time.Time
}

func (a MembershipApplication) ToTypes() *types.MembershipApplication {
	return &types.MembershipApplication{
		ID:           conv.Pointer(strfmt.UUID4(a.ID)),
		Email:        conv.Pointer(strfmt.Email(a.Email)),
		FullName:     conv.Pointer(a.FullName),
		Motivation:   conv.Pointer(a.Motivation),
		Organisation: a.Organisation.Ptr(),
		Status:       conv.Pointer(a.Status),
		ReviewNote:   a.ReviewNote.Ptr(),
		CreatedAt:    conv.Pointer(strfmt.DateTime(a.CreatedAt)),
	}
}

type MembershipApplicationList []MembershipApplication

func (l MembershipApplicationList) ToTypes() *types.MembershipApplicationList {
	items := make([]*types.MembershipApplication, len(l))
	for i := range l {
		items[i] = l[i].ToTypes()
	}

	return &types.MembershipApplicationList{Items: items}
}

type ApproveMembershipRequest struct {
	ApplicationID string
	ReviewerID    string
	Note          string
}

// ApproveMembershipResult carries the PLAINTEXT invitation token, which exists
// only in memory on its way into the invitation email. Only its hash is stored.
type ApproveMembershipResult struct {
	Application     MembershipApplication
	InvitationToken string
}

type AcceptMembershipRequest struct {
	InvitationToken string
	Password        string
}

// PurgeResult reports what the retention sweep removed.
type PurgeResult struct {
	ContactMessages        int64
	MembershipApplications int64
}

// --- Donations --------------------------------------------------------------

type DonationTier struct {
	ID          string
	Name        string
	Description string
	AmountKobo  int64
	Interval    string
}

type DonationTierList []DonationTier

func (l DonationTierList) ToTypes() *types.DonationTierList {
	items := make([]*types.DonationTier, len(l))
	for i := range l {
		items[i] = &types.DonationTier{
			ID:          conv.Pointer(strfmt.UUID4(l[i].ID)),
			Name:        conv.Pointer(l[i].Name),
			Description: l[i].Description,
			AmountKobo:  conv.Pointer(l[i].AmountKobo),
			Interval:    l[i].Interval,
		}
	}

	return &types.DonationTierList{Items: items}
}

type StartDonationRequest struct {
	Email       string
	AmountKobo  int64
	DisplayName null.String
	IsAnonymous bool
	UserID      null.String
}

// StartDonationResult carries the PLAINTEXT access token, which exists only in
// memory on its way to the donor. Only its hash is stored.
type StartDonationResult struct {
	Reference        string
	AuthorizationURL string
	AccessToken      string
	AmountKobo       int64
}

func (r StartDonationResult) ToTypes() *types.DonationCheckoutResponse {
	return &types.DonationCheckoutResponse{
		Reference:        conv.Pointer(r.Reference),
		AuthorizationURL: conv.Pointer(r.AuthorizationURL),
		AccessToken:      conv.Pointer(r.AccessToken),
		AmountKobo:       conv.Pointer(r.AmountKobo),
	}
}

type DonationStatus struct {
	Reference  string
	Status     string
	AmountKobo int64
	Currency   string
}

func (d DonationStatus) ToTypes() *types.DonationStatusResponse {
	return &types.DonationStatusResponse{
		Reference:  conv.Pointer(d.Reference),
		Status:     conv.Pointer(d.Status),
		AmountKobo: conv.Pointer(d.AmountKobo),
		Currency:   conv.Pointer(d.Currency),
	}
}

type Donation struct {
	ID          string
	Reference   string
	Status      string
	AmountKobo  int64
	Currency    string
	Email       string
	DisplayName null.String
	IsAnonymous bool
	CreatedAt   time.Time
}

type DonationList []Donation

func (l DonationList) ToTypes() *types.DonationList {
	items := make([]*types.Donation, len(l))
	for i := range l {
		items[i] = &types.Donation{
			ID:          conv.Pointer(strfmt.UUID4(l[i].ID)),
			Reference:   conv.Pointer(l[i].Reference),
			Status:      conv.Pointer(l[i].Status),
			AmountKobo:  conv.Pointer(l[i].AmountKobo),
			Currency:    conv.Pointer(l[i].Currency),
			Email:       conv.Pointer(strfmt.Email(l[i].Email)),
			DisplayName: l[i].DisplayName.Ptr(),
			IsAnonymous: conv.Pointer(l[i].IsAnonymous),
			CreatedAt:   conv.Pointer(strfmt.DateTime(l[i].CreatedAt)),
		}
	}

	return &types.DonationList{Items: items}
}

// --- Ticketing --------------------------------------------------------------

type EventRegistrationRequest struct {
	EventSlug string
	TierID    string
	Email     string
	FullName  string
	Quantity  int
	UserID    null.String
}

// EventRegistrationResult describes what happened. A free RSVP comes back
// confirmed with no reference; a paid one comes back reserved with somewhere to
// pay and a token for checking on it afterwards.
type EventRegistrationResult struct {
	TicketCode       string
	Status           string
	Reference        string
	AuthorizationURL string
	AccessToken      string
	AmountKobo       int64
}

func (r EventRegistrationResult) ToTypes() *types.EventRegistrationResponse {
	result := &types.EventRegistrationResponse{
		TicketCode: conv.Pointer(r.TicketCode),
		Status:     conv.Pointer(r.Status),
		AmountKobo: conv.Pointer(r.AmountKobo),
	}

	if r.Reference != "" {
		result.Reference = r.Reference
		result.AuthorizationURL = r.AuthorizationURL
		result.AccessToken = r.AccessToken
	}

	return result
}

type TicketTier struct {
	ID          string
	Name        string
	Description string
	PriceKobo   int64
	Currency    string
	Remaining   int
	MaxPerOrder int
}

type TicketTierList []TicketTier

func (l TicketTierList) ToTypes() *types.TicketTierList {
	items := make([]*types.TicketTier, len(l))
	for i := range l {
		items[i] = &types.TicketTier{
			ID:          conv.Pointer(strfmt.UUID4(l[i].ID)),
			Name:        conv.Pointer(l[i].Name),
			Description: l[i].Description,
			PriceKobo:   conv.Pointer(l[i].PriceKobo),
			Currency:    conv.Pointer(l[i].Currency),
			Remaining:   conv.Pointer(int64(l[i].Remaining)),
			MaxPerOrder: conv.Pointer(int64(l[i].MaxPerOrder)),
		}
	}

	return &types.TicketTierList{Items: items}
}

// --- Merch ------------------------------------------------------------------

type ProductVariant struct {
	ID        string
	SKU       string
	Size      null.String
	Colour    null.String
	PriceKobo int64
	Available int
}

type Product struct {
	ID          string
	Slug        string
	Title       string
	Description string
	ImageURL    null.String
	Variants    []ProductVariant
}

type ProductList []Product

func (l ProductList) ToTypes() *types.ProductList {
	items := make([]*types.Product, len(l))

	for i := range l {
		variants := make([]*types.ProductVariant, len(l[i].Variants))
		for variantIndex, variant := range l[i].Variants {
			variants[variantIndex] = &types.ProductVariant{
				ID:        conv.Pointer(strfmt.UUID4(variant.ID)),
				Sku:       conv.Pointer(variant.SKU),
				Size:      variant.Size.Ptr(),
				Colour:    variant.Colour.Ptr(),
				PriceKobo: conv.Pointer(variant.PriceKobo),
				Available: conv.Pointer(int64(variant.Available)),
			}
		}

		items[i] = &types.Product{
			ID:          conv.Pointer(strfmt.UUID4(l[i].ID)),
			Slug:        conv.Pointer(l[i].Slug),
			Title:       conv.Pointer(l[i].Title),
			Description: l[i].Description,
			Image:       l[i].ImageURL.Ptr(),
			Variants:    variants,
		}
	}

	return &types.ProductList{Items: items}
}

// CartLine is what the client sends: an id and a count, and nothing else.
// Prices are never accepted from the client.
type CartLine struct {
	VariantID string
	Quantity  int
}

// CartProblemReason explains why one line could not be fulfilled.
type CartProblemReason string

const (
	CartProblemUnavailable CartProblemReason = "unavailable"
	CartProblemOutOfStock  CartProblemReason = "out_of_stock"
)

// CartProblem names ONE failed line. The cart is client-side, so the frontend
// has to be told which item is the problem -- otherwise a single sold-out size
// shows as "something went wrong" for the whole basket.
type CartProblem struct {
	VariantID string
	Reason    CartProblemReason
	Available int
}

type CheckoutRequest struct {
	Lines            []CartLine
	Email            string
	FullName         string
	Phone            null.String
	ShippingLine1    null.String
	ShippingLine2    null.String
	ShippingCity     null.String
	ShippingState    string
	ShippingCountry  null.String
	ShippingPostcode null.String
	UserID           null.String
}

type CheckoutResult struct {
	Reference        string
	AuthorizationURL string
	AccessToken      string
	Totals           money.Totals
	Problems         []CartProblem
}

func (r CheckoutResult) ToTypes() *types.CheckoutResponse {
	return &types.CheckoutResponse{
		Reference:        conv.Pointer(r.Reference),
		AuthorizationURL: conv.Pointer(r.AuthorizationURL),
		AccessToken:      conv.Pointer(r.AccessToken),
		SubtotalKobo:     conv.Pointer(r.Totals.SubtotalKobo),
		ShippingKobo:     conv.Pointer(r.Totals.ShippingKobo),
		TaxKobo:          conv.Pointer(r.Totals.TaxKobo),
		TotalKobo:        conv.Pointer(r.Totals.TotalKobo),
	}
}

// ConflictToTypes renders the 409 body, which keeps the standard error envelope
// and adds the per-line detail a plain `detail` string cannot carry.
func (r CheckoutResult) ConflictToTypes() *types.CheckoutConflictResponse {
	problems := make([]*types.CartProblem, len(r.Problems))
	for i := range r.Problems {
		problems[i] = &types.CartProblem{
			VariantID: conv.Pointer(strfmt.UUID4(r.Problems[i].VariantID)),
			Reason:    conv.Pointer(string(r.Problems[i].Reason)),
			Available: conv.Pointer(int64(r.Problems[i].Available)),
		}
	}

	return &types.CheckoutConflictResponse{
		Status:   conv.Pointer(int64(http.StatusConflict)),
		Type:     types.PublicHTTPErrorTypeGeneric.Pointer(),
		Title:    conv.Pointer("Some items in your cart are no longer available."),
		Problems: problems,
	}
}

// StartRecurringDonationRequest begins a subscription.
//
// There is no amount: it comes from the tier, because the provider fixes an
// amount to a Plan and accepting one from the client would silently disagree
// with what is actually charged.
type StartRecurringDonationRequest struct {
	TierID      string
	Email       string
	DisplayName null.String
	IsAnonymous bool
	UserID      null.String
}

// --- Admin operations -------------------------------------------------------

type AdvanceOrderRequest struct {
	OrderID           string
	To                string
	TrackingReference null.String
	ActorUserID       null.String
	ActorEmail        string
	SourceIP          string
}

type OrderSummary struct {
	ID        string
	Reference string
	Status    string
	TotalKobo int64
	Email     string
	CreatedAt time.Time
}

type OrderSummaryList []OrderSummary

func (l OrderSummaryList) ToTypes() *types.OrderList {
	items := make([]*types.OrderSummary, len(l))
	for i := range l {
		items[i] = l[i].ToTypes()
	}

	return &types.OrderList{Items: items}
}

func (o OrderSummary) ToTypes() *types.OrderSummary {
	return &types.OrderSummary{
		ID:        conv.Pointer(strfmt.UUID4(o.ID)),
		Reference: conv.Pointer(o.Reference),
		Status:    conv.Pointer(o.Status),
		TotalKobo: conv.Pointer(o.TotalKobo),
		Email:     conv.Pointer(strfmt.Email(o.Email)),
		CreatedAt: conv.Pointer(strfmt.DateTime(o.CreatedAt)),
	}
}

// RefundRequest asks for money back. Restock is carried explicitly because
// whether goods return is a human judgement, not something inferable from a
// refund.
type RefundRequest struct {
	PaymentID   string
	AmountKobo  int64
	Reason      string
	Restock     bool
	ActorUserID null.String
	ActorEmail  string
	SourceIP    string
}

type RefundResult struct {
	PaymentID         string
	AmountKobo        int64
	TotalRefundedKobo int64
	Status            string
}

func (r RefundResult) ToTypes() *types.RefundResponse {
	return &types.RefundResponse{
		PaymentID:         conv.Pointer(strfmt.UUID4(r.PaymentID)),
		AmountKobo:        conv.Pointer(r.AmountKobo),
		TotalRefundedKobo: conv.Pointer(r.TotalRefundedKobo),
		Status:            conv.Pointer(r.Status),
	}
}

// AuditEntry is one privileged action. Snapshots are whole objects rather than a
// diff, because a diff is only interpretable against a schema that may since
// have changed.
type AuditEntry struct {
	ActorUserID null.String
	ActorEmail  string
	Action      string
	SubjectType string
	SubjectID   string
	Before      map[string]any
	After       map[string]any
	SourceIP    string
}

type WebhookEventSummary struct {
	ID                string
	Provider          string
	EventType         string
	ProviderReference null.String
	ReceivedAt        time.Time
	Attempts          int
}

type WebhookEventList []WebhookEventSummary

func (l WebhookEventList) ToTypes() *types.WebhookEventList {
	items := make([]*types.WebhookEventSummary, len(l))
	for i := range l {
		items[i] = &types.WebhookEventSummary{
			ID:                conv.Pointer(strfmt.UUID4(l[i].ID)),
			Provider:          conv.Pointer(l[i].Provider),
			EventType:         conv.Pointer(l[i].EventType),
			ProviderReference: l[i].ProviderReference.Ptr(),
			ReceivedAt:        conv.Pointer(strfmt.DateTime(l[i].ReceivedAt)),
			Attempts:          conv.Pointer(int64(l[i].Attempts)),
		}
	}

	return &types.WebhookEventList{Items: items}
}
