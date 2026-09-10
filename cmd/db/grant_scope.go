package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/auth"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/config"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/command"
	dbutil "github.com/idowuseyi/cloudtecko-backend-go/internal/util/db"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// knownScopes is the allowlist. A typo'd scope would otherwise be granted
// silently and simply never match anything, which looks identical to a
// permissions bug and is tedious to diagnose.
var knownScopes = []string{
	auth.ScopeApp.String(),
	auth.ScopeAdmin.String(),
}

var errUserNotFound = errors.New("no user with that username")

func newGrantScope() *cobra.Command {
	var (
		username string
		scope    string
		revoke   bool
	)

	cmd := &cobra.Command{
		Use:   "grant-scope",
		Short: "Grants (or revokes) an auth scope for a user.",
		Long: `Adds a scope to users.scopes, which is what middleware.AuthWithConfig
checks when guarding a route group.

This is how the FIRST admin is created: there is deliberately no environment
variable that auto-grants admin at boot, because that turns a leaked env file
into privilege escalation and makes the grant invisible in any audit trail.

  app db grant-scope --username someone@example.com --scope admin
  app db grant-scope --username someone@example.com --scope admin --revoke`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return grantScopeCmdFunc(username, scope, revoke)
		},
	}

	cmd.Flags().StringVar(&username, "username", "", "username (email) of the user to modify")
	cmd.Flags().StringVar(&scope, "scope", "", fmt.Sprintf("scope to grant or revoke %v", knownScopes))
	cmd.Flags().BoolVar(&revoke, "revoke", false, "remove the scope instead of adding it")

	if err := cmd.MarkFlagRequired("username"); err != nil {
		log.Fatal().Err(err).Msg("Failed to mark username flag required")
	}

	if err := cmd.MarkFlagRequired("scope"); err != nil {
		log.Fatal().Err(err).Msg("Failed to mark scope flag required")
	}

	return cmd
}

func grantScopeCmdFunc(username string, scope string, revoke bool) error {
	if !slices.Contains(knownScopes, scope) {
		return fmt.Errorf("unknown scope %q, expected one of %v", scope, knownScopes)
	}

	err := command.WithServer(context.Background(), config.DefaultServiceConfigFromEnv(), func(ctx context.Context, s *api.Server) error {
		return dbutil.WithTransaction(ctx, s.DB, func(exec boil.ContextExecutor) error {
			return applyScopeChange(ctx, exec, username, scope, revoke)
		})
	})
	if err != nil {
		return fmt.Errorf("failed to change scope: %w", err)
	}

	return nil
}

func applyScopeChange(ctx context.Context, exec boil.ContextExecutor, username string, scope string, revoke bool) error {
	log := util.LogFromContext(ctx)

	user, err := models.Users(
		models.UserWhere.Username.EQ(null.StringFrom(username)),
	).One(ctx, exec)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errUserNotFound
		}

		return fmt.Errorf("failed to load user: %w", err)
	}

	has := slices.Contains(user.Scopes, scope)
	switch {
	case revoke && !has, !revoke && has:
		log.Info().Str("username", username).Str("scope", scope).Bool("revoke", revoke).
			Msg("Scope already in the requested state, nothing to do")

		return nil
	case revoke:
		user.Scopes = slices.DeleteFunc(user.Scopes, func(s string) bool { return s == scope })
	default:
		user.Scopes = append(user.Scopes, scope)
	}

	if _, err := user.Update(ctx, exec, boil.Whitelist(
		models.UserColumns.Scopes,
		models.UserColumns.UpdatedAt,
	)); err != nil {
		return fmt.Errorf("failed to update user scopes: %w", err)
	}

	log.Info().Str("username", username).Str("scope", scope).Bool("revoke", revoke).
		Strs("scopes", user.Scopes).Msg("Updated user scopes")

	return nil
}
