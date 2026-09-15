package ticketing_test

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/config"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test/fixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHighContentionNeverOversells runs far more contention than the unit-level
// race test, against a deliberately awkward capacity.
//
// 60 concurrent buyers against 7 seats, in mixed quantities. The mixed
// quantities matter: a reservation of 2 against 1 remaining seat must fail
// cleanly rather than partially succeed, which a capacity check written as
// "> 0" rather than "+ qty <= total" would get wrong while still passing every
// single-seat test.
func TestHighContentionNeverOversells(t *testing.T) {
	t.Parallel()

	// Rate limiting OFF for this test specifically.
	//
	// The limiter is per-IP and defaults to 10 requests/second, so 60 requests
	// from one test client are mostly rejected with 429 before the reservation
	// code is ever reached -- which would make this test pass while measuring
	// nothing. Disabling it here isolates the property under test. The limiter
	// itself is exercised by its own middleware tests.
	config := config.DefaultServiceConfigFromEnv()
	config.Echo.EnableRateLimitMiddleware = false

	test.WithTestServerConfigurable(t, config, func(s *api.Server) {
		ctx := t.Context()
		fix := fixtures.Fixtures()

		const (
			capacity = 7
			buyers   = 60
		)

		tier, err := models.FindEventTicketTier(ctx, s.DB, fix.TierPaid.ID)
		require.NoError(t, err)

		tier.QuantityTotal = capacity
		tier.MaxPerOrder = 3
		_, err = tier.Update(ctx, s.DB, boil.Whitelist(
			models.EventTicketTierColumns.QuantityTotal,
			models.EventTicketTierColumns.MaxPerOrder,
			models.EventTicketTierColumns.UpdatedAt,
		))
		require.NoError(t, err)

		var (
			wg       sync.WaitGroup
			mutex    sync.Mutex
			seatsWon int
			statuses = map[int]int{}
		)

		wg.Add(buyers)

		for i := range buyers {
			go func(n int) {
				defer wg.Done()

				// 1, 2 or 3 seats -- so the last seat can be contested by a request
				// that cannot fit.
				quantity := (n % 3) + 1

				res := register(t, s, fix.TierPaid.ID, fmt.Sprintf("load-%d@example.com", n), quantity)

				mutex.Lock()
				defer mutex.Unlock()

				statuses[res.StatusCode]++

				if res.StatusCode == http.StatusCreated {
					seatsWon += quantity
				}
			}(i)
		}

		wg.Wait()

		for status, count := range statuses {
			assert.Contains(t, []int{http.StatusCreated, http.StatusConflict}, status,
				"unexpected status %d (x%d): a deadlock or serialization failure would surface here", status, count)
		}

		assert.LessOrEqual(t, seatsWon, capacity,
			"sold %d seats against a capacity of %d -- this is an oversell", seatsWon, capacity)

		final, err := models.FindEventTicketTier(ctx, s.DB, fix.TierPaid.ID)
		require.NoError(t, err)

		assert.LessOrEqual(t, final.QuantityReserved+final.QuantitySold, final.QuantityTotal,
			"reserved(%d) + sold(%d) exceeded capacity(%d)",
			final.QuantityReserved, final.QuantitySold, final.QuantityTotal)

		assert.GreaterOrEqual(t, final.QuantityReserved, 0, "a counter must never go negative")
		assert.GreaterOrEqual(t, final.QuantitySold, 0)

		// The counters must agree with the registrations actually written: a
		// reservation that incremented the counter without persisting a row would
		// silently lose a seat forever.
		registrations, err := models.EventRegistrations(
			models.EventRegistrationWhere.TicketTierID.EQ(fix.TierPaid.ID),
			models.EventRegistrationWhere.Status.IN([]string{
				models.RegistrationStatusReserved,
				models.RegistrationStatusConfirmed,
			}),
		).All(ctx, s.DB)
		require.NoError(t, err)

		recorded := 0
		for _, registration := range registrations {
			recorded += registration.Quantity
		}

		assert.Equal(t, seatsWon, recorded,
			"counters and registrations disagree: %d seats reported sold, %d recorded", seatsWon, recorded)
	})
}
