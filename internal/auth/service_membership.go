package auth

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
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/hashing"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/password"
)

// ApproveMembershipApplication approves an application and mints an invitation.
//
// It creates NO user. Only the hash of the invitation token is stored, matching
// what S-018 and S-024 established for every other credential-bearing token
// here: the value handed to a human never exists in the database.
//
// Creating the user at this point and mailing a confirmation link -- the obvious
// shortcut, since CompleteRegister already exists -- would be wrong twice over.
// The link would be a bearer credential (whoever opens the email is signed in,
// and mail is forwarded, shared and prefetched by scanners), and the account
// would have no password, so Login would reject it forever once the session
// expired and UpdatePassword would refuse it as a non-local user.
func (s *Service) ApproveMembershipApplication(ctx context.Context, request dto.ApproveMembershipRequest) (dto.ApproveMembershipResult, error) {
	log := util.LogFromContext(ctx)

	var result dto.ApproveMembershipResult

	err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		application, err := models.FindMembershipApplication(ctx, exec, request.ApplicationID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return httperrors.ErrNotFoundApplication
			}

			return fmt.Errorf("failed to load membership application: %w", err)
		}

		if application.Status != models.ApplicationStatusPending {
			return httperrors.ErrConflictApplicationNotPending
		}

		plaintext, tokenHash, err := hashing.NewToken()
		if err != nil {
			log.Err(err).Msg("Failed to generate invitation token")

			return fmt.Errorf("failed to generate invitation token: %w", err)
		}

		application.Status = models.ApplicationStatusApproved
		application.InvitationTokenHash = null.BytesFrom(tokenHash)
		application.InvitationValidUntil = null.TimeFrom(s.clock.Now().Add(s.config.Auth.InvitationTokenValidity))
		application.ReviewedByUserID = null.StringFrom(request.ReviewerID)
		application.ReviewedAt = null.TimeFrom(s.clock.Now())
		application.ReviewNote = null.NewString(request.Note, request.Note != "")

		if _, err := application.Update(ctx, exec, boil.Whitelist(
			models.MembershipApplicationColumns.Status,
			models.MembershipApplicationColumns.InvitationTokenHash,
			models.MembershipApplicationColumns.InvitationValidUntil,
			models.MembershipApplicationColumns.ReviewedByUserID,
			models.MembershipApplicationColumns.ReviewedAt,
			models.MembershipApplicationColumns.ReviewNote,
			models.MembershipApplicationColumns.UpdatedAt,
		)); err != nil {
			log.Err(err).Msg("Failed to approve membership application")

			return fmt.Errorf("failed to approve membership application: %w", err)
		}

		result = dto.ApproveMembershipResult{
			Application: dto.MembershipApplication{
				ID:           application.ID,
				Email:        application.Email,
				FullName:     application.FullName,
				Motivation:   application.Motivation,
				Organisation: application.Organisation,
				Status:       application.Status,
				ReviewNote:   application.ReviewNote,
				CreatedAt:    application.CreatedAt,
			},
			InvitationToken: plaintext,
		}

		return nil
	})
	if err != nil {
		return dto.ApproveMembershipResult{}, err
	}

	return result, nil
}

// AcceptMembershipInvitation turns an approved application into an account.
//
// The user row is created HERE, at acceptance, with a password the applicant
// chose -- not at approval. A rejected or never-accepted application therefore
// leaves no orphan account, and the invitation link alone grants nothing without
// also setting a password.
func (s *Service) AcceptMembershipInvitation(ctx context.Context, request dto.AcceptMembershipRequest) (dto.LoginResult, error) {
	log := util.LogFromContext(ctx)

	var result dto.LoginResult

	err := db.WithTransaction(ctx, s.db, func(exec boil.ContextExecutor) error {
		application, err := models.MembershipApplications(
			models.MembershipApplicationWhere.InvitationTokenHash.EQ(null.BytesFrom(hashing.HashToken(request.InvitationToken))),
			models.MembershipApplicationWhere.Status.EQ(models.ApplicationStatusApproved),
			models.MembershipApplicationWhere.InvitationValidUntil.GT(null.TimeFrom(s.clock.Now())),
			// Serialise concurrent acceptances of the same invitation: without the
			// row lock two requests both find it live and both create an account,
			// and the unique index on username then fails the second with a 500
			// instead of a clean "already used".
			qm.For("UPDATE"),
		).One(ctx, exec)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				log.Debug().Msg("Invitation token not found, not approved, or expired")

				return httperrors.ErrNotFoundInvitationInvalid
			}

			return fmt.Errorf("failed to load invitation: %w", err)
		}

		username := dto.NewUsername(application.Email)

		if err := password.Validate(request.Password, s.passwordPolicy(username.String())); err != nil {
			log.Debug().Err(err).Msg("Rejected invitation password: does not meet the password policy")

			return httperrors.ErrBadRequestInvalidPassword
		}

		hash, err := hashing.HashPassword(request.Password, hashing.DefaultArgon2ParamsFromEnv())
		if err != nil {
			log.Err(err).Msg("Failed to hash password")

			return httperrors.ErrBadRequestInvalidPassword
		}

		user := &models.User{
			Username:            null.StringFrom(username.String()),
			Password:            null.StringFrom(hash),
			LastAuthenticatedAt: null.TimeFrom(s.clock.Now()),
			// Active immediately: an admin already vetted this person, and the
			// invitation token proves control of the mailbox, so a second email
			// confirmation would verify nothing new.
			IsActive:             true,
			RequiresConfirmation: false,
			Scopes:               s.config.Auth.DefaultUserScopes,
		}

		if err := user.Insert(ctx, exec, boil.Infer()); err != nil {
			if db.IsUniqueViolation(err, "users_username_key") {
				return httperrors.ErrConflictUserAlreadyExists
			}

			log.Err(err).Msg("Failed to insert user from invitation")

			return fmt.Errorf("failed to insert user: %w", err)
		}

		appUserProfile := models.AppUserProfile{UserID: user.ID}
		if err := appUserProfile.Insert(ctx, exec, boil.Infer()); err != nil {
			log.Err(err).Msg("Failed to insert app user profile")

			return fmt.Errorf("failed to insert app user profile: %w", err)
		}

		application.Status = models.ApplicationStatusAccepted
		application.AcceptedUserID = null.StringFrom(user.ID)
		// Clear the token: it is single-use, and a consumed invitation must not
		// remain matchable.
		application.InvitationTokenHash = null.Bytes{}
		application.InvitationValidUntil = null.Time{}

		if _, err := application.Update(ctx, exec, boil.Whitelist(
			models.MembershipApplicationColumns.Status,
			models.MembershipApplicationColumns.AcceptedUserID,
			models.MembershipApplicationColumns.InvitationTokenHash,
			models.MembershipApplicationColumns.InvitationValidUntil,
			models.MembershipApplicationColumns.UpdatedAt,
		)); err != nil {
			log.Err(err).Msg("Failed to mark application accepted")

			return fmt.Errorf("failed to mark application accepted: %w", err)
		}

		result, err = s.authenticateUser(ctx, exec, dto.AuthenticateUserRequest{
			User: mapper.LocalUserToDTO(user),
		})
		if err != nil {
			log.Err(err).Msg("Failed to authenticate invited user")

			return fmt.Errorf("failed to authenticate invited user: %w", err)
		}

		return nil
	})
	if err != nil {
		return dto.LoginResult{}, err
	}

	return result, nil
}
