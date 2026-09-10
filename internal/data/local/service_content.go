package local

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/httperrors"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/mapper"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/db"
)

// publishedEventMods restricts a query to events the public may see.
//
// Both halves matter: published_at set (it has been released) AND archived_at
// unset (it has not been retired). There is no deleted_at anywhere in this
// schema precisely so that "visible" is expressed once, here, rather than as a
// filter every future query must remember.
func publishedEventMods() []qm.QueryMod {
	return []qm.QueryMod{
		models.EventWhere.PublishedAt.IsNotNull(),
		models.EventWhere.ArchivedAt.IsNull(),
	}
}

func publishedResourceMods() []qm.QueryMod {
	return []qm.QueryMod{
		models.ResourceWhere.PublishedAt.IsNotNull(),
		models.ResourceWhere.ArchivedAt.IsNull(),
	}
}

// categoryFilterMods adds a category filter by SLUG rather than by id, so a URL
// carries a readable "?category=workshop" instead of a uuid.
func categoryFilterMods(slug string, relation string) []qm.QueryMod {
	if slug == "" {
		return nil
	}

	return []qm.QueryMod{
		qm.InnerJoin(fmt.Sprintf("categories c_filter ON c_filter.id = %s.category_id", relation)),
		qm.Where("c_filter.slug = ?", slug),
	}
}

// ListEvents returns a page of published events plus the category tabs.
func (s *Service) ListEvents(ctx context.Context, request dto.ContentListRequest) (dto.GalleryPage, error) {
	log := util.LogFromContext(ctx)

	mods := publishedEventMods()
	mods = append(mods, categoryFilterMods(request.CategorySlug.String, models.TableNames.Events)...)

	total, err := models.Events(mods...).Count(ctx, s.db)
	if err != nil {
		log.Err(err).Msg("Failed to count events")

		return dto.GalleryPage{}, fmt.Errorf("failed to count events: %w", err)
	}

	listMods := append([]qm.QueryMod{}, mods...)
	listMods = append(listMods,
		qm.Load(models.EventRels.Category),
		qm.Load(models.EventRels.HeroMedium),
		qm.OrderBy(models.EventColumns.StartAt+" DESC"),
		qm.Limit(int(request.PerPage)),
		qm.Offset(request.Offset()),
	)

	events, err := models.Events(listMods...).All(ctx, s.db)
	if err != nil {
		log.Err(err).Msg("Failed to list events")

		return dto.GalleryPage{}, fmt.Errorf("failed to list events: %w", err)
	}

	items := make([]dto.GalleryItem, len(events))
	for i, event := range events {
		// Ticket aggregates arrive in Phase 6. Until then every event reads as
		// Open, which is correct for one that does not sell tickets.
		items[i] = mapper.LocalEventToGalleryItem(event, 0, 0)
	}

	categories, err := s.listCategoryNames(ctx, models.CategoryScopeEvent)
	if err != nil {
		return dto.GalleryPage{}, err
	}

	return dto.GalleryPage{
		Items:      items,
		Categories: categories,
		Total:      total,
		Page:       request.Page,
		PerPage:    request.PerPage,
	}, nil
}

// ListResources returns a page of published resources, ordered by full-text
// relevance when a search term is present and by publication date otherwise.
func (s *Service) ListResources(ctx context.Context, request dto.ContentListRequest) (dto.GalleryPage, error) {
	log := util.LogFromContext(ctx)

	// An empty tsQuery means "no usable search terms" -- punctuation only, say.
	// That degrades to an unfiltered list rather than an error, so a visitor
	// typing a stray backslash gets results instead of a 500.
	tsQuery := db.SearchStringToTSQuery(request.Search.Ptr())

	mods := publishedResourceMods()
	mods = append(mods, categoryFilterMods(request.CategorySlug.String, models.TableNames.Resources)...)

	if tsQuery != "" {
		mods = append(mods, db.SearchTSVector(tsQuery, models.TableNames.Resources, db.TSVectorColumnSearchTSV))
	}

	total, err := models.Resources(mods...).Count(ctx, s.db)
	if err != nil {
		log.Err(err).Msg("Failed to count resources")

		return dto.GalleryPage{}, fmt.Errorf("failed to count resources: %w", err)
	}

	listMods := append([]qm.QueryMod{}, mods...)
	listMods = append(listMods,
		qm.Load(models.ResourceRels.Category),
		qm.Load(models.ResourceRels.HeroMedium),
	)

	if tsQuery != "" {
		listMods = append(listMods, db.SearchTSVectorRank(tsQuery, models.TableNames.Resources, db.TSVectorColumnSearchTSV))
	} else {
		listMods = append(listMods, qm.OrderBy(models.ResourceColumns.PublishedAt+" DESC"))
	}

	listMods = append(listMods,
		qm.Limit(int(request.PerPage)),
		qm.Offset(request.Offset()),
	)

	resources, err := models.Resources(listMods...).All(ctx, s.db)
	if err != nil {
		log.Err(err).Msg("Failed to list resources")

		return dto.GalleryPage{}, fmt.Errorf("failed to list resources: %w", err)
	}

	items := make([]dto.GalleryItem, len(resources))
	for i, resource := range resources {
		items[i] = mapper.LocalResourceToGalleryItem(resource)
	}

	categories, err := s.listCategoryNames(ctx, models.CategoryScopeResource)
	if err != nil {
		return dto.GalleryPage{}, err
	}

	return dto.GalleryPage{
		Items:      items,
		Categories: categories,
		Total:      total,
		Page:       request.Page,
		PerPage:    request.PerPage,
	}, nil
}

// GetEventBySlug returns one published event.
func (s *Service) GetEventBySlug(ctx context.Context, slug string) (dto.EventDetail, error) {
	log := util.LogFromContext(ctx)

	mods := publishedEventMods()
	mods = append(mods,
		models.EventWhere.Slug.EQ(slug),
		qm.Load(models.EventRels.Category),
		qm.Load(models.EventRels.HeroMedium),
	)

	event, err := models.Events(mods...).One(ctx, s.db)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Debug().Str("slug", slug).Msg("Event not found")

			return dto.EventDetail{}, httperrors.ErrNotFoundEvent
		}

		log.Err(err).Msg("Failed to load event")

		return dto.EventDetail{}, fmt.Errorf("failed to load event: %w", err)
	}

	return mapper.LocalEventToDTO(event, 0, 0), nil
}

// GetFeaturedEvent returns the single featured event.
//
// A partial unique index guarantees at most one exists, so this cannot silently
// pick an arbitrary row when an admin has misconfigured two.
func (s *Service) GetFeaturedEvent(ctx context.Context) (dto.EventDetail, error) {
	log := util.LogFromContext(ctx)

	mods := publishedEventMods()
	mods = append(mods,
		models.EventWhere.IsFeatured.EQ(true),
		qm.Load(models.EventRels.Category),
		qm.Load(models.EventRels.HeroMedium),
	)

	event, err := models.Events(mods...).One(ctx, s.db)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Debug().Msg("No featured event")

			return dto.EventDetail{}, httperrors.ErrNotFoundEvent
		}

		log.Err(err).Msg("Failed to load featured event")

		return dto.EventDetail{}, fmt.Errorf("failed to load featured event: %w", err)
	}

	return mapper.LocalEventToDTO(event, 0, 0), nil
}

// GetResourceBySlug returns one published resource.
func (s *Service) GetResourceBySlug(ctx context.Context, slug string) (dto.ResourceDetail, error) {
	log := util.LogFromContext(ctx)

	mods := publishedResourceMods()
	mods = append(mods,
		models.ResourceWhere.Slug.EQ(slug),
		qm.Load(models.ResourceRels.Category),
		qm.Load(models.ResourceRels.HeroMedium),
	)

	resource, err := models.Resources(mods...).One(ctx, s.db)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Debug().Str("slug", slug).Msg("Resource not found")

			return dto.ResourceDetail{}, httperrors.ErrNotFoundResource
		}

		log.Err(err).Msg("Failed to load resource")

		return dto.ResourceDetail{}, fmt.Errorf("failed to load resource: %w", err)
	}

	return mapper.LocalResourceToDTO(resource), nil
}

func (s *Service) listCategoryNames(ctx context.Context, scope string) ([]string, error) {
	log := util.LogFromContext(ctx)

	categories, err := models.Categories(
		models.CategoryWhere.Scope.EQ(scope),
		qm.OrderBy(models.CategoryColumns.SortOrder+", "+models.CategoryColumns.Name),
	).All(ctx, s.db)
	if err != nil {
		log.Err(err).Msg("Failed to list categories")

		return nil, fmt.Errorf("failed to list categories: %w", err)
	}

	names := make([]string, len(categories))
	for i, category := range categories {
		names[i] = category.Name
	}

	return names, nil
}
