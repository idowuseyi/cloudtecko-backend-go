package mapper

import (
	"github.com/aarondl/null/v8"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
)

// heroImageURL resolves the public URL of an eagerly-loaded hero asset, falling
// back to an empty string. The frontend treats an empty image as "use the
// placeholder", so a missing asset degrades to a layout that still renders
// rather than a broken card.
func heroImageURL(media *models.MediaAsset) null.String {
	if media == nil || !media.PublicURL.Valid {
		return null.String{}
	}

	return media.PublicURL
}

func categoryName(category *models.Category) string {
	if category == nil {
		return ""
	}

	return category.Name
}

// LocalEventToGalleryItem projects an event onto the shared gallery shape.
//
// sold and capacity come from the event's ticket tiers and are aggregated by the
// caller; status is computed here rather than stored, because it changes on every
// ticket sale. Until ticketing exists (Phase 6) callers pass 0/0, which reads as
// Open -- the correct answer for an event that does not sell tickets.
func LocalEventToGalleryItem(event *models.Event, sold int, capacity int) dto.GalleryItem {
	var category *models.Category

	var hero *models.MediaAsset

	if event.R != nil {
		category = event.R.Category
		hero = event.R.HeroMedium
	}

	return dto.GalleryItem{
		ID:          event.Slug,
		Category:    categoryName(category),
		Date:        event.StartAt,
		Title:       event.Title,
		Description: event.Summary,
		ImageURL:    heroImageURL(hero).String,
		Href:        null.StringFrom("/events/" + event.Slug),
		Status:      dto.DeriveGalleryStatus(sold, capacity),
	}
}

// LocalResourceToGalleryItem projects a resource onto the shared gallery shape.
// Status is deliberately absent: availability is meaningless for a resource.
func LocalResourceToGalleryItem(resource *models.Resource) dto.GalleryItem {
	var category *models.Category

	var hero *models.MediaAsset

	if resource.R != nil {
		category = resource.R.Category
		hero = resource.R.HeroMedium
	}

	date := resource.CreatedAt
	if resource.PublishedAt.Valid {
		date = resource.PublishedAt.Time
	}

	return dto.GalleryItem{
		ID:          resource.Slug,
		Category:    categoryName(category),
		Date:        date,
		Title:       resource.Title,
		Description: resource.Summary,
		ImageURL:    heroImageURL(hero).String,
		Href:        null.StringFrom("/resources/" + resource.Slug),
	}
}

func LocalEventToDTO(event *models.Event, sold int, capacity int) dto.EventDetail {
	var category *models.Category

	var hero *models.MediaAsset

	if event.R != nil {
		category = event.R.Category
		hero = event.R.HeroMedium
	}

	return dto.EventDetail{
		ID:           event.ID,
		Slug:         event.Slug,
		Title:        event.Title,
		Summary:      event.Summary,
		Body:         event.Body,
		Category:     categoryName(category),
		ImageURL:     heroImageURL(hero),
		StartAt:      event.StartAt,
		EndAt:        event.EndAt,
		Timezone:     event.Timezone,
		IsOnline:     event.IsOnline,
		VenueName:    event.VenueName,
		VenueAddress: event.VenueAddress,
		MeetingURL:   event.MeetingURL,
		IsFeatured:   event.IsFeatured,
		Status:       dto.DeriveGalleryStatus(sold, capacity),
	}
}

func LocalResourceToDTO(resource *models.Resource) dto.ResourceDetail {
	var category *models.Category

	var hero *models.MediaAsset

	if resource.R != nil {
		category = resource.R.Category
		hero = resource.R.HeroMedium
	}

	return dto.ResourceDetail{
		ID:                 resource.ID,
		Slug:               resource.Slug,
		Title:              resource.Title,
		Summary:            resource.Summary,
		Body:               resource.Body,
		Kind:               resource.Kind,
		Category:           categoryName(category),
		ImageURL:           heroImageURL(hero),
		Author:             resource.Author,
		ContentURL:         resource.ContentURL,
		ReadingTimeMinutes: resource.ReadingTimeMinutes,
		PublishedAt:        resource.PublishedAt,
	}
}
