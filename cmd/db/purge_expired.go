package db

import (
	"context"
	"fmt"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/config"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/command"
	"github.com/spf13/cobra"
)

func newPurgeExpired() *cobra.Command {
	return &cobra.Command{
		Use:   "purge-expired",
		Short: "Hard-deletes personal data whose retention window has closed.",
		Long: `Removes contact messages and membership applications past their purge_after.

Deletion is HARD, not soft: soft-deleted personal data is still personal data
under the NDPA 2023, and a "deleted" flag that every query must remember to
filter is a leak waiting to happen.

There is deliberately no lazy path for this. Unlike a reservation hold -- which
is released opportunistically because the next checkout on the same row needs it
gone -- nothing about serving a request depends on expired personal data being
absent, so nothing would ever trigger the sweep. Schedule it:

  app db purge-expired      # e.g. Render cron, daily`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return purgeExpiredCmdFunc()
		},
	}
}

func purgeExpiredCmdFunc() error {
	err := command.WithServer(context.Background(), config.DefaultServiceConfigFromEnv(), func(ctx context.Context, s *api.Server) error {
		log := util.LogFromContext(ctx)

		result, err := s.Local.PurgeExpiredPII(ctx)
		if err != nil {
			return err
		}

		log.Info().
			Int64("contactMessages", result.ContactMessages).
			Int64("membershipApplications", result.MembershipApplications).
			Msg("Purged expired personal data")

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to purge expired data: %w", err)
	}

	return nil
}
