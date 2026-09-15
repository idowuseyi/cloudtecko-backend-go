package ticketing_test

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test/fixtures"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const eventSlug = "aws-certification-workshop-lagos"

func register(t *testing.T, s *api.Server, tierID string, email string, quantity int) *http.Response {
	t.Helper()

	return test.PerformRequest(t, s, "POST", "/api/v1/events/"+eventSlug+"/register", test.GenericPayload{
		"tierId":   tierID,
		"email":    email,
		"fullName": "Test Attendee",
		"quantity": quantity,
	}, nil).Result()
}

// A free tier confirms immediately: there is nothing to pay, so there is nothing
// to wait for.
func TestFreeTierConfirmsImmediately(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		res := test.PerformRequest(t, s, "POST", "/api/v1/events/"+eventSlug+"/register", test.GenericPayload{
			"tierId": fix.TierFree.ID, "email": "free@example.com", "fullName": "Free Attendee", "quantity": 1,
		}, nil)
		require.Equal(t, http.StatusCreated, res.Result().StatusCode)

		var result types.EventRegistrationResponse
		test.ParseResponseAndValidate(t, res, &result)

		assert.Equal(t, models.RegistrationStatusConfirmed, *result.Status)
		assert.Equal(t, int64(0), *result.AmountKobo)
		assert.Regexp(t, `^CLD-[A-Z2-9]{5}-[A-Z2-9]{5}$`, *result.TicketCode)
		assert.Empty(t, result.AuthorizationURL, "a free RSVP has nowhere to pay")

		// Seats moved straight from reserved to sold.
		tier, err := models.FindEventTicketTier(t.Context(), s.DB, fix.TierFree.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, tier.QuantitySold)
		assert.Equal(t, 0, tier.QuantityReserved)
	})
}

// A paid tier reserves and waits. Whether payment is required follows from the
// TIER, never the request, so a client cannot ask to skip it.
func TestPaidTierReservesAndReturnsSomewhereToPay(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		res := test.PerformRequest(t, s, "POST", "/api/v1/events/"+eventSlug+"/register", test.GenericPayload{
			"tierId": fix.TierPaid.ID, "email": "paid@example.com", "fullName": "Paying Attendee", "quantity": 1,
		}, nil)
		require.Equal(t, http.StatusCreated, res.Result().StatusCode)

		var result types.EventRegistrationResponse
		test.ParseResponseAndValidate(t, res, &result)

		assert.Equal(t, models.RegistrationStatusReserved, *result.Status)
		assert.Equal(t, int64(500000), *result.AmountKobo)
		assert.NotEmpty(t, result.AuthorizationURL)
		assert.NotEmpty(t, result.AccessToken)

		tier, err := models.FindEventTicketTier(t.Context(), s.DB, fix.TierPaid.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, tier.QuantityReserved, "the seat is held, not sold")
		assert.Equal(t, 0, tier.QuantitySold)
	})
}

func TestSoldOutTierIsRefused(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		res := register(t, s, fix.TierSoldOut.ID, "late@example.com", 1)
		assert.Equal(t, http.StatusConflict, res.StatusCode)
	})
}

func TestQuantityAboveMaxPerOrderIsRefused(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		// The tier caps an order at 2; without that cap one caller could reserve
		// an entire event in a single request.
		res := register(t, s, fix.TierPaid.ID, "greedy@example.com", 3)
		assert.Equal(t, http.StatusBadRequest, res.StatusCode)
	})
}

// A double-clicked RSVP must not become two seats.
func TestSecondRegistrationForTheSameEmailIsRefused(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		require.Equal(t, http.StatusCreated, register(t, s, fix.TierFree.ID, "twice@example.com", 1).StatusCode)

		second := register(t, s, fix.TierFree.ID, "twice@example.com", 1)
		assert.Equal(t, http.StatusConflict, second.StatusCode)
	})
}

// THE test for this phase. Unit tests cannot catch an oversell: it only appears
// when two requests genuinely race for the last seat.
//
// Ten concurrent registrations against a tier with three seats must produce
// exactly three successes -- no more, because that is an oversell, and no fewer,
// because that is lost revenue from a lock held too coarsely.
func TestConcurrentRegistrationsNeverOversell(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		const attempts = 10

		var (
			wg        sync.WaitGroup
			mutex     sync.Mutex
			created   int
			conflicts int
			other     []int
		)

		wg.Add(attempts)

		for i := range attempts {
			go func(n int) {
				defer wg.Done()

				res := register(t, s, fix.TierPaid.ID, fmt.Sprintf("racer-%d@example.com", n), 1)

				mutex.Lock()
				defer mutex.Unlock()

				switch res.StatusCode {
				case http.StatusCreated:
					created++
				case http.StatusConflict:
					conflicts++
				default:
					other = append(other, res.StatusCode)
				}
			}(i)
		}

		wg.Wait()

		assert.Empty(t, other, "every attempt must be a clean 201 or 409, got %v", other)
		assert.Equal(t, fix.TierPaid.QuantityTotal, created,
			"exactly the available seats must be sold: more is an oversell, fewer is lost revenue")
		assert.Equal(t, attempts-fix.TierPaid.QuantityTotal, conflicts)

		tier, err := models.FindEventTicketTier(t.Context(), s.DB, fix.TierPaid.ID)
		require.NoError(t, err)
		assert.LessOrEqual(t, tier.QuantityReserved+tier.QuantitySold, tier.QuantityTotal,
			"reserved + sold must never exceed capacity through the reservation path")
	})
}

// Remaining must subtract holds as well as sales: a seat someone is part-way
// through paying for is not available, even though no money has arrived.
func TestRemainingSubtractsInFlightHolds(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		require.Equal(t, http.StatusCreated, register(t, s, fix.TierPaid.ID, "holder@example.com", 1).StatusCode)

		res := test.PerformRequest(t, s, "GET", "/api/v1/events/"+eventSlug+"/tiers", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var tiers types.TicketTierList
		test.ParseResponseAndValidate(t, res, &tiers)

		for _, tier := range tiers.Items {
			if tier.ID.String() == fix.TierPaid.ID {
				assert.Equal(t, int64(fix.TierPaid.QuantityTotal-1), *tier.Remaining,
					"an in-flight hold must reduce the advertised remaining count")
			}
		}
	})
}

// Remaining must never be reported as negative, which is reachable after an
// oversell.
func TestRemainingIsNeverNegative(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/api/v1/events/"+eventSlug+"/tiers", nil, nil)
		require.Equal(t, http.StatusOK, res.Result().StatusCode)

		var tiers types.TicketTierList
		test.ParseResponseAndValidate(t, res, &tiers)

		require.NotEmpty(t, tiers.Items)

		for _, tier := range tiers.Items {
			assert.GreaterOrEqual(t, *tier.Remaining, int64(0))
		}
	})
}

func TestRegisterForUnknownEventIs404(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		res := test.PerformRequest(t, s, "POST", "/api/v1/events/no-such-event/register", test.GenericPayload{
			"tierId": fix.TierFree.ID, "email": "x@example.com", "fullName": "X", "quantity": 1,
		}, nil)
		assert.Equal(t, http.StatusNotFound, res.Result().StatusCode)
	})
}

// An unpublished event must not be registrable, even with a valid tier id.
func TestRegisterForUnpublishedEventIs404(t *testing.T) {
	t.Parallel()

	test.WithTestServer(t, func(s *api.Server) {
		fix := fixtures.Fixtures()

		res := test.PerformRequest(t, s, "POST", "/api/v1/events/unpublished-draft-event/register", test.GenericPayload{
			"tierId": fix.TierFree.ID, "email": "x@example.com", "fullName": "X", "quantity": 1,
		}, nil)
		assert.Equal(t, http.StatusNotFound, res.Result().StatusCode)
	})
}
