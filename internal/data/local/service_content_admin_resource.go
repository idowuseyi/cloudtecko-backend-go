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

// CreateResource creates an unpublished resource.
func (s *Service) CreateResource(ctx context.Context, request dto.ResourceWriteRequest) (dto.ResourceDetail, error) {
	log := util.LogFromContext(ctx)

	var result dto.ResourceDetail

	err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		category, err := s.requireCategory(ctx, exec, models.CategoryScopeResource, request.CategorySlug)
		if err != nil {
			return err
		}

		desired, err := s.availableResourceSlug(ctx, exec, slug.Make(request.Title))
		if err != nil {
			return err
		}

		resource := &models.Resource{
			Slug:               desired,
			Title:              request.Title,
			Summary:            request.Summary,
			Body:               request.Body,
			Kind:               request.Kind,
			CategoryID:         category.ID,
			HeroMediaID:        request.HeroMediaID,
			Author:             request.Author,
			ContentURL:         request.ContentURL,
			ReadingTimeMinutes: request.ReadingTimeMinutes,
		}

		if err := resource.Insert(ctx, exec, boil.Infer()); err != nil {
			if db.IsUniqueViolation(err, "resources_slug_key") {
				return httperrors.ErrConflictSlugTaken
			}

			log.Err(err).Msg("Failed to insert resource")

			return fmt.Errorf("failed to insert resource: %w", err)
		}

		resource.R = resource.R.NewStruct()
		resource.R.Category = category
		result = mapper.LocalResourceToDTO(resource)

		return nil
	})
	if err != nil {
		return dto.ResourceDetail{}, err
	}

	return result, nil
}

// UpdateResource edits a resource in place; the slug stays put.
func (s *Service) UpdateResource(ctx context.Context, resourceID string, request dto.ResourceWriteRequest) (dto.ResourceDetail, error) {
	return s.mutateResource(ctx, resourceID, func(ctx context.Context, exec boil.ContextExecutor, resource *models.Resource) (*models.Category, error) {
		category, err := s.requireCategory(ctx, exec, models.CategoryScopeResource, request.CategorySlug)
		if err != nil {
			return nil, err
		}

		resource.Title = request.Title
		resource.Summary = request.Summary
		resource.Body = request.Body
		resource.Kind = request.Kind
		resource.CategoryID = category.ID
		resource.HeroMediaID = request.HeroMediaID
		resource.Author = request.Author
		resource.ContentURL = request.ContentURL
		resource.ReadingTimeMinutes = request.ReadingTimeMinutes

		return category, nil
	})
}

// SetResourcePublished toggles publication.
func (s *Service) SetResourcePublished(ctx context.Context, resourceID string, published bool) (dto.ResourceDetail, error) {
	return s.mutateResource(ctx, resourceID, func(ctx context.Context, exec boil.ContextExecutor, resource *models.Resource) (*models.Category, error) {
		if published {
			if !resource.PublishedAt.Valid {
				resource.PublishedAt = null.TimeFrom(s.clock.Now())
			}
		} else {
			resource.PublishedAt = null.Time{}
		}

		return s.loadCategory(ctx, exec, resource.CategoryID)
	})
}

// SetResourceArchived toggles archival.
func (s *Service) SetResourceArchived(ctx context.Context, resourceID string, archived bool) (dto.ResourceDetail, error) {
	return s.mutateResource(ctx, resourceID, func(ctx context.Context, exec boil.ContextExecutor, resource *models.Resource) (*models.Category, error) {
		if archived {
			resource.ArchivedAt = null.TimeFrom(s.clock.Now())
		} else {
			resource.ArchivedAt = null.Time{}
		}

		return s.loadCategory(ctx, exec, resource.CategoryID)
	})
}

// DeleteResource hard-deletes an unpublished resource.
func (s *Service) DeleteResource(ctx context.Context, resourceID string) error {
	log := util.LogFromContext(ctx)

	return db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		resource, err := s.requireResource(ctx, exec, resourceID)
		if err != nil {
			return err
		}

		if resource.PublishedAt.Valid {
			return httperrors.ErrConflictDeletePublished
		}

		if _, err := resource.Delete(ctx, exec); err != nil {
			log.Err(err).Msg("Failed to delete resource")

			return fmt.Errorf("failed to delete resource: %w", err)
		}

		return nil
	})
}

func (s *Service) mutateResource(
	ctx context.Context,
	resourceID string,
	mutate func(context.Context, boil.ContextExecutor, *models.Resource) (*models.Category, error),
) (dto.ResourceDetail, error) {
	log := util.LogFromContext(ctx)

	var result dto.ResourceDetail

	err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		resource, err := s.requireResource(ctx, exec, resourceID)
		if err != nil {
			return err
		}

		category, err := mutate(ctx, exec, resource)
		if err != nil {
			return err
		}

		// Whitelist every writable column explicitly. boil.Infer() would skip Go
		// zero values, so clearing published_at or archived_at back to NULL -- the
		// unpublish and restore paths -- would be silently dropped.
		if _, err := resource.Update(ctx, exec, boil.Whitelist(
			models.ResourceColumns.Title,
			models.ResourceColumns.Summary,
			models.ResourceColumns.Body,
			models.ResourceColumns.Kind,
			models.ResourceColumns.CategoryID,
			models.ResourceColumns.HeroMediaID,
			models.ResourceColumns.Author,
			models.ResourceColumns.ContentURL,
			models.ResourceColumns.ReadingTimeMinutes,
			models.ResourceColumns.PublishedAt,
			models.ResourceColumns.ArchivedAt,
			models.ResourceColumns.UpdatedAt,
		)); err != nil {
			log.Err(err).Msg("Failed to update resource")

			return fmt.Errorf("failed to update resource: %w", err)
		}

		resource.R = resource.R.NewStruct()
		resource.R.Category = category
		result = mapper.LocalResourceToDTO(resource)

		return nil
	})
	if err != nil {
		return dto.ResourceDetail{}, err
	}

	return result, nil
}

func (s *Service) requireResource(ctx context.Context, exec boil.ContextExecutor, resourceID string) (*models.Resource, error) {
	resource, err := models.FindResource(ctx, exec, resourceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, httperrors.ErrNotFoundResource
		}

		return nil, fmt.Errorf("failed to load resource: %w", err)
	}

	return resource, nil
}

func (s *Service) availableResourceSlug(ctx context.Context, exec boil.ContextExecutor, base string) (string, error) {
	existing, err := models.Resources(
		qm.Select(models.ResourceColumns.Slug),
		qm.Where(models.ResourceColumns.Slug+" = ? OR "+models.ResourceColumns.Slug+" LIKE ?", base, db.EscapeLike(base)+"-%"),
	).All(ctx, exec)
	if err != nil {
		return "", fmt.Errorf("failed to query existing slugs: %w", err)
	}

	taken := make(map[string]struct{}, len(existing))
	for _, resource := range existing {
		taken[resource.Slug] = struct{}{}
	}

	return slug.NextAvailable(base, taken), nil
}

// --- Categories -------------------------------------------------------------

func (s *Service) ListCategories(ctx context.Context) (dto.CategoryList, error) {
	categories, err := models.Categories(
		qm.OrderBy(models.CategoryColumns.Scope+", "+models.CategoryColumns.SortOrder+", "+models.CategoryColumns.Name),
	).All(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("failed to list categories: %w", err)
	}

	result := make(dto.CategoryList, len(categories))
	for i, category := range categories {
		result[i] = dto.Category{
			ID:        category.ID,
			Scope:     category.Scope,
			Slug:      category.Slug,
			Name:      category.Name,
			SortOrder: category.SortOrder,
		}
	}

	return result, nil
}

func (s *Service) CreateCategory(ctx context.Context, request dto.CategoryWriteRequest) (dto.Category, error) {
	log := util.LogFromContext(ctx)

	category := &models.Category{
		Scope:     request.Scope,
		Slug:      request.Slug,
		Name:      request.Name,
		SortOrder: request.SortOrder,
	}

	if err := category.Insert(ctx, s.db, boil.Infer()); err != nil {
		if db.IsUniqueViolation(err, "categories_scope_slug_key") {
			return dto.Category{}, httperrors.ErrConflictSlugTaken
		}

		log.Err(err).Msg("Failed to insert category")

		return dto.Category{}, fmt.Errorf("failed to insert category: %w", err)
	}

	return dto.Category{
		ID:        category.ID,
		Scope:     category.Scope,
		Slug:      category.Slug,
		Name:      category.Name,
		SortOrder: category.SortOrder,
	}, nil
}

// DeleteCategory removes a category.
//
// The FK from events and resources is ON DELETE RESTRICT, so a category still in
// use cannot be removed. That surfaces as a 409 rather than a 500: refusing is
// the correct behaviour, and the alternative -- cascading -- would silently
// delete published content.
func (s *Service) DeleteCategory(ctx context.Context, resourceID string) error {
	log := util.LogFromContext(ctx)

	category, err := models.FindCategory(ctx, s.db, resourceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return httperrors.ErrNotFoundCategory
		}

		return fmt.Errorf("failed to load category: %w", err)
	}

	if _, err := category.Delete(ctx, s.db); err != nil {
		if db.IsForeignKeyViolation(err, "") {
			return httperrors.ErrConflictCategoryInUse
		}

		log.Err(err).Msg("Failed to delete category")

		return fmt.Errorf("failed to delete category: %w", err)
	}

	return nil
}
