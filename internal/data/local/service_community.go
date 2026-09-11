package local

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/httperrors"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/db"
)

// SubmitContactMessage records a contact-form submission.
//
// Returns no indication of whether the sender has written before. The per-email
// throttle silently drops a flood rather than reporting it, because telling a
// sender "you have submitted too recently" would confirm that an address has
// used the form.
func (s *Service) SubmitContactMessage(ctx context.Context, request dto.ContactMessageRequest) error {
	log := util.LogFromContext(ctx)

	recentlySubmitted, err := models.ContactMessages(
		qm.Where("lower(email) = ?", strings.ToLower(request.Email)),
		models.ContactMessageWhere.CreatedAt.GT(s.clock.Now().Add(-s.config.Community.ContactThrottle)),
	).Exists(ctx, s.db)
	if err != nil {
		log.Err(err).Msg("Failed to check contact throttle")

		return fmt.Errorf("failed to check contact throttle: %w", err)
	}

	if recentlySubmitted {
		log.Debug().Msg("Dropping contact message inside the per-email throttle window")

		return nil
	}

	message := &models.ContactMessage{
		Name:           request.Name,
		Email:          request.Email,
		Subject:        request.Subject,
		Message:        request.Message,
		Status:         models.ContactMessageStatusNew,
		SourceIPPrefix: null.NewString(TruncateIP(request.SourceIP), request.SourceIP != ""),
		UserAgent:      null.NewString(request.UserAgent, request.UserAgent != ""),
		PurgeAfter:     s.clock.Now().Add(s.config.Community.ContactRetention),
	}

	if err := message.Insert(ctx, s.db, boil.Infer()); err != nil {
		log.Err(err).Msg("Failed to insert contact message")

		return fmt.Errorf("failed to insert contact message: %w", err)
	}

	return nil
}

// Network-prefix widths used when storing a client address for abuse triage.
// Wide enough to group an ISP customer or a datacentre tenant, narrow enough to
// stop being an identifier for one person.
const (
	ipv4PrefixBits = 24
	ipv4AddrBits   = 32
	ipv6PrefixBits = 48
	ipv6AddrBits   = 128
)

// TruncateIP reduces an address to a /24 (IPv4) or /48 (IPv6) prefix.
//
// The prefix is enough to correlate abuse from one network; the full address is
// personal data that buys nothing extra for that purpose.
func TruncateIP(address string) string {
	if address == "" {
		return ""
	}

	parsed := net.ParseIP(address)
	if parsed == nil {
		return ""
	}

	if v4 := parsed.To4(); v4 != nil {
		return v4.Mask(net.CIDRMask(ipv4PrefixBits, ipv4AddrBits)).String() + "/24"
	}

	return parsed.Mask(net.CIDRMask(ipv6PrefixBits, ipv6AddrBits)).String() + "/48"
}

// ListFAQs returns published FAQs, optionally narrowed to one category.
func (s *Service) ListFAQs(ctx context.Context, category null.String) (dto.FAQList, error) {
	mods := []qm.QueryMod{
		models.FaqWhere.PublishedAt.IsNotNull(),
		qm.OrderBy(models.FaqColumns.Category + ", " + models.FaqColumns.SortOrder),
	}

	if category.Valid && category.String != "" {
		mods = append(mods, models.FaqWhere.Category.EQ(category.String))
	}

	faqs, err := models.Faqs(mods...).All(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("failed to list faqs: %w", err)
	}

	result := make(dto.FAQList, len(faqs))
	for i, faq := range faqs {
		result[i] = dto.FAQ{
			ID:       faq.ID,
			Question: faq.Question,
			Answer:   faq.Answer,
			Category: faq.Category,
		}
	}

	return result, nil
}

// SubmitMembershipApplication records a waitlist application.
//
// It returns nil for a new application, a duplicate, and a previously rejected
// address alike. The EMAIL sent afterwards differs; the RESPONSE never does.
// Anything else turns this endpoint into a membership-list oracle, which is the
// same defect the security audit recorded against /register as S-020.
func (s *Service) SubmitMembershipApplication(ctx context.Context, request dto.MembershipApplicationRequest) error {
	log := util.LogFromContext(ctx)

	application := &models.MembershipApplication{
		Email:        request.Email,
		FullName:     request.FullName,
		Motivation:   request.Motivation,
		Organisation: null.NewString(request.Organisation, request.Organisation != ""),
		Status:       models.ApplicationStatusPending,
		PurgeAfter:   s.clock.Now().Add(s.config.Community.ApplicationRetention),
	}

	if err := application.Insert(ctx, s.db, boil.Infer()); err != nil {
		// A live application already exists for this address. That is a normal
		// outcome, not an error the caller should learn about.
		if db.IsUniqueViolation(err, "idx_membership_applications_one_live_per_email") {
			log.Debug().Msg("Membership application already live for this address")

			return nil
		}

		log.Err(err).Msg("Failed to insert membership application")

		return fmt.Errorf("failed to insert membership application: %w", err)
	}

	return nil
}

// ListMembershipApplications returns applications for admin review.
func (s *Service) ListMembershipApplications(ctx context.Context, status null.String) (dto.MembershipApplicationList, error) {
	mods := []qm.QueryMod{
		qm.OrderBy(models.MembershipApplicationColumns.CreatedAt + " DESC"),
	}

	if status.Valid && status.String != "" {
		mods = append(mods, models.MembershipApplicationWhere.Status.EQ(status.String))
	}

	applications, err := models.MembershipApplications(mods...).All(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("failed to list membership applications: %w", err)
	}

	result := make(dto.MembershipApplicationList, len(applications))
	for i, application := range applications {
		result[i] = dto.MembershipApplication{
			ID:           application.ID,
			Email:        application.Email,
			FullName:     application.FullName,
			Motivation:   application.Motivation,
			Organisation: application.Organisation,
			Status:       application.Status,
			ReviewNote:   application.ReviewNote,
			CreatedAt:    application.CreatedAt,
		}
	}

	return result, nil
}

// RejectMembershipApplication records a decision without creating any account.
func (s *Service) RejectMembershipApplication(ctx context.Context, applicationID string, reviewerID string, note string) (dto.MembershipApplication, error) {
	log := util.LogFromContext(ctx)

	var result dto.MembershipApplication

	err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		application, err := s.requireApplication(ctx, exec, applicationID)
		if err != nil {
			return err
		}

		if application.Status != models.ApplicationStatusPending {
			return httperrors.ErrConflictApplicationNotPending
		}

		application.Status = models.ApplicationStatusRejected
		application.ReviewedByUserID = null.StringFrom(reviewerID)
		application.ReviewedAt = null.TimeFrom(s.clock.Now())
		application.ReviewNote = null.NewString(note, note != "")

		if _, err := application.Update(ctx, exec, boil.Whitelist(
			models.MembershipApplicationColumns.Status,
			models.MembershipApplicationColumns.ReviewedByUserID,
			models.MembershipApplicationColumns.ReviewedAt,
			models.MembershipApplicationColumns.ReviewNote,
			models.MembershipApplicationColumns.UpdatedAt,
		)); err != nil {
			log.Err(err).Msg("Failed to reject membership application")

			return fmt.Errorf("failed to reject membership application: %w", err)
		}

		result = dto.MembershipApplication{
			ID:         application.ID,
			Email:      application.Email,
			FullName:   application.FullName,
			Status:     application.Status,
			ReviewNote: application.ReviewNote,
			CreatedAt:  application.CreatedAt,
		}

		return nil
	})
	if err != nil {
		return dto.MembershipApplication{}, err
	}

	return result, nil
}

func (s *Service) requireApplication(ctx context.Context, exec boil.ContextExecutor, applicationID string) (*models.MembershipApplication, error) {
	application, err := models.FindMembershipApplication(ctx, exec, applicationID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, httperrors.ErrNotFoundApplication
		}

		return nil, fmt.Errorf("failed to load membership application: %w", err)
	}

	return application, nil
}

// PurgeExpiredPII hard-deletes rows whose retention window has closed.
//
// Hard, not soft: soft-deleted personal data is still personal data. Run by
// `app db purge-expired` on a schedule -- there is no lazy path for this,
// because unlike a reservation hold, nothing about serving a request depends on
// expired PII being gone, so nothing would ever trigger it opportunistically.
func (s *Service) PurgeExpiredPII(ctx context.Context) (dto.PurgeResult, error) {
	now := s.clock.Now()

	contactDeleted, err := models.ContactMessages(
		models.ContactMessageWhere.PurgeAfter.LT(now),
	).DeleteAll(ctx, s.db)
	if err != nil {
		return dto.PurgeResult{}, fmt.Errorf("failed to purge contact messages: %w", err)
	}

	applicationsDeleted, err := models.MembershipApplications(
		models.MembershipApplicationWhere.PurgeAfter.LT(now),
	).DeleteAll(ctx, s.db)
	if err != nil {
		return dto.PurgeResult{}, fmt.Errorf("failed to purge membership applications: %w", err)
	}

	return dto.PurgeResult{
		ContactMessages:        contactDeleted,
		MembershipApplications: applicationsDeleted,
	}, nil
}
