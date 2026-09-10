package local

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/httperrors"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/mapper"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/db"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/slug"
)

// CreateEvent creates an unpublished event. Publication is a separate, explicit
// step so that saving a draft can never put a half-written event on the site.
func (s *Service) CreateEvent(ctx context.Context, request dto.EventWriteRequest) (dto.EventDetail, error) {
	log := util.LogFromContext(ctx)

	var result dto.EventDetail

	err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		category, err := s.requireCategory(ctx, exec, models.CategoryScopeEvent, request.CategorySlug)
		if err != nil {
			return err
		}

		desired, err := s.availableEventSlug(ctx, exec, slug.Make(request.Title))
		if err != nil {
			return err
		}

		event := &models.Event{
			Slug:         desired,
			Title:        request.Title,
			Summary:      request.Summary,
			Body:         request.Body,
			CategoryID:   category.ID,
			HeroMediaID:  request.HeroMediaID,
			StartAt:      request.StartAt,
			EndAt:        request.EndAt,
			Timezone:     request.Timezone,
			IsOnline:     request.IsOnline,
			VenueName:    request.VenueName,
			VenueAddress: request.VenueAddress,
			MeetingURL:   request.MeetingURL,
		}

		if err := event.Insert(ctx, exec, boil.Infer()); err != nil {
			// The unique index is the backstop for a concurrent writer taking the
			// slug between availableEventSlug and here. Rare, and honest: a 409 the
			// admin can retry beats silently choosing a different URL.
			if db.IsUniqueViolation(err, "events_slug_key") {
				return httperrors.ErrConflictSlugTaken
			}

			if db.IsCheckViolation(err, "events_time_order_check") {
				return httperrors.ErrBadRequestEventTimeOrder
			}

			log.Err(err).Msg("Failed to insert event")

			return fmt.Errorf("failed to insert event: %w", err)
		}

		event.R = event.R.NewStruct()
		event.R.Category = category
		result = mapper.LocalEventToDTO(event, 0, 0)

		return nil
	})
	if err != nil {
		return dto.EventDetail{}, err
	}

	return result, nil
}

// UpdateEvent edits an event in place. The slug is deliberately NOT recomputed
// from a changed title: once published, the slug is a permanent public address.
func (s *Service) UpdateEvent(ctx context.Context, eventID string, request dto.EventWriteRequest) (dto.EventDetail, error) {
	log := util.LogFromContext(ctx)

	var result dto.EventDetail

	err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		event, err := s.requireEvent(ctx, exec, eventID)
		if err != nil {
			return err
		}

		category, err := s.requireCategory(ctx, exec, models.CategoryScopeEvent, request.CategorySlug)
		if err != nil {
			return err
		}

		event.Title = request.Title
		event.Summary = request.Summary
		event.Body = request.Body
		event.CategoryID = category.ID
		event.HeroMediaID = request.HeroMediaID
		event.StartAt = request.StartAt
		event.EndAt = request.EndAt
		event.Timezone = request.Timezone
		event.IsOnline = request.IsOnline
		event.VenueName = request.VenueName
		event.VenueAddress = request.VenueAddress
		event.MeetingURL = request.MeetingURL

		if _, err := event.Update(ctx, exec, boil.Infer()); err != nil {
			if db.IsCheckViolation(err, "events_time_order_check") {
				return httperrors.ErrBadRequestEventTimeOrder
			}

			log.Err(err).Msg("Failed to update event")

			return fmt.Errorf("failed to update event: %w", err)
		}

		event.R = event.R.NewStruct()
		event.R.Category = category
		result = mapper.LocalEventToDTO(event, 0, 0)

		return nil
	})
	if err != nil {
		return dto.EventDetail{}, err
	}

	return result, nil
}

// SetEventPublished publishes or unpublishes an event.
func (s *Service) SetEventPublished(ctx context.Context, eventID string, published bool) (dto.EventDetail, error) {
	return s.mutateEvent(ctx, eventID, func(event *models.Event) error {
		if published {
			if !event.PublishedAt.Valid {
				event.PublishedAt = null.TimeFrom(s.clock.Now())
			}

			return nil
		}

		event.PublishedAt = null.Time{}
		// Unpublishing must also clear the featured flag. The partial unique index
		// only covers published rows, so an unpublished-but-featured event would
		// leave the slot free AND silently re-take it on republish, producing two
		// featured events by way of a constraint that cannot see either.
		event.IsFeatured = false

		return nil
	})
}

// SetEventArchived retires or restores an event.
func (s *Service) SetEventArchived(ctx context.Context, eventID string, archived bool) (dto.EventDetail, error) {
	return s.mutateEvent(ctx, eventID, func(event *models.Event) error {
		if archived {
			event.ArchivedAt = null.TimeFrom(s.clock.Now())
			event.IsFeatured = false

			return nil
		}

		event.ArchivedAt = null.Time{}

		return nil
	})
}

// SetEventFeatured promotes an event to the single featured slot.
//
// Demotion of the incumbent and promotion of the new event happen in ONE
// transaction. The partial unique index refuses two featured published rows, so
// doing this in two requests would be a guaranteed 23505 for the admin; doing it
// in one transaction makes the swap atomic and the constraint invisible.
func (s *Service) SetEventFeatured(ctx context.Context, eventID string, featured bool) (dto.EventDetail, error) {
	log := util.LogFromContext(ctx)

	var result dto.EventDetail

	err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		event, err := s.requireEvent(ctx, exec, eventID)
		if err != nil {
			return err
		}

		if featured {
			if !event.PublishedAt.Valid {
				return httperrors.ErrConflictFeatureUnpublished
			}

			if _, err := models.Events(
				models.EventWhere.IsFeatured.EQ(true),
				models.EventWhere.ID.NEQ(event.ID),
			).UpdateAll(ctx, exec, models.M{models.EventColumns.IsFeatured: false}); err != nil {
				log.Err(err).Msg("Failed to demote previously featured events")

				return fmt.Errorf("failed to demote featured events: %w", err)
			}
		}

		event.IsFeatured = featured
		if _, err := event.Update(ctx, exec, boil.Whitelist(
			models.EventColumns.IsFeatured,
			models.EventColumns.UpdatedAt,
		)); err != nil {
			log.Err(err).Msg("Failed to set featured flag")

			return fmt.Errorf("failed to set featured flag: %w", err)
		}

		category, err := s.loadCategory(ctx, exec, event.CategoryID)
		if err != nil {
			return err
		}

		event.R = event.R.NewStruct()
		event.R.Category = category
		result = mapper.LocalEventToDTO(event, 0, 0)

		return nil
	})
	if err != nil {
		return dto.EventDetail{}, err
	}

	return result, nil
}

// DeleteEvent hard-deletes an event. Only unpublished drafts may be deleted:
// a published event may already be referenced by registrations and shared links,
// and this schema has no soft delete by design, so archiving is the operation
// for anything the public has seen.
func (s *Service) DeleteEvent(ctx context.Context, eventID string) error {
	log := util.LogFromContext(ctx)

	return db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		event, err := s.requireEvent(ctx, exec, eventID)
		if err != nil {
			return err
		}

		if event.PublishedAt.Valid {
			return httperrors.ErrConflictDeletePublished
		}

		if _, err := event.Delete(ctx, exec); err != nil {
			log.Err(err).Msg("Failed to delete event")

			return fmt.Errorf("failed to delete event: %w", err)
		}

		return nil
	})
}

func (s *Service) mutateEvent(ctx context.Context, eventID string, mutate func(*models.Event) error) (dto.EventDetail, error) {
	log := util.LogFromContext(ctx)

	var result dto.EventDetail

	err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		event, err := s.requireEvent(ctx, exec, eventID)
		if err != nil {
			return err
		}

		if err := mutate(event); err != nil {
			return err
		}

		if _, err := event.Update(ctx, exec, boil.Infer()); err != nil {
			log.Err(err).Msg("Failed to update event")

			return fmt.Errorf("failed to update event: %w", err)
		}

		category, err := s.loadCategory(ctx, exec, event.CategoryID)
		if err != nil {
			return err
		}

		event.R = event.R.NewStruct()
		event.R.Category = category
		result = mapper.LocalEventToDTO(event, 0, 0)

		return nil
	})
	if err != nil {
		return dto.EventDetail{}, err
	}

	return result, nil
}

func (s *Service) requireEvent(ctx context.Context, exec boil.ContextExecutor, eventID string) (*models.Event, error) {
	event, err := models.FindEvent(ctx, exec, eventID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, httperrors.ErrNotFoundEvent
		}

		return nil, fmt.Errorf("failed to load event: %w", err)
	}

	return event, nil
}

func (s *Service) requireCategory(ctx context.Context, exec boil.ContextExecutor, scope string, categorySlug string) (*models.Category, error) {
	category, err := models.Categories(
		models.CategoryWhere.Scope.EQ(scope),
		models.CategoryWhere.Slug.EQ(categorySlug),
	).One(ctx, exec)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, httperrors.ErrNotFoundCategory
		}

		return nil, fmt.Errorf("failed to load category: %w", err)
	}

	return category, nil
}

func (s *Service) loadCategory(ctx context.Context, exec boil.ContextExecutor, categoryID string) (*models.Category, error) {
	category, err := models.FindCategory(ctx, exec, categoryID)
	if err != nil {
		return nil, fmt.Errorf("failed to load category: %w", err)
	}

	return category, nil
}

// availableEventSlug picks the first free slug in the base, base-2, base-3 series
// using ONE prefix query. See slug.NextAvailable for why this is not an
// insert-and-retry loop.
func (s *Service) availableEventSlug(ctx context.Context, exec boil.ContextExecutor, base string) (string, error) {
	existing, err := models.Events(
		qm.Select(models.EventColumns.Slug),
		qm.Where(models.EventColumns.Slug+" = ? OR "+models.EventColumns.Slug+" LIKE ?", base, db.EscapeLike(base)+"-%"),
	).All(ctx, exec)
	if err != nil {
		return "", fmt.Errorf("failed to query existing slugs: %w", err)
	}

	taken := make(map[string]struct{}, len(existing))
	for _, event := range existing {
		taken[event.Slug] = struct{}{}
	}

	return slug.NextAvailable(base, taken), nil
}
