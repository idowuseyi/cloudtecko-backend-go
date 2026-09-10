package dto

import (
	"time"

	"github.com/aarondl/null/v8"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/swag/conv"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
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
