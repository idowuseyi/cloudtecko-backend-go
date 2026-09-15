package money_test

import (
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/money"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The default posture: not registered for VAT, so no tax is added and the
// breakdown is just goods plus shipping.
func TestComputeTotalsWithNoTax(t *testing.T) {
	t.Parallel()

	totals, err := money.ComputeTotals(500000, 150000, 0, 0)
	require.NoError(t, err)

	assert.Equal(t, int64(0), totals.TaxKobo)
	assert.Equal(t, int64(650000), totals.TotalKobo)
}

// Nigerian VAT is 7.5%, i.e. 750 basis points.
func TestComputeTotalsWithNigerianVAT(t *testing.T) {
	t.Parallel()

	totals, err := money.ComputeTotals(500000, 150000, 0, 750)
	require.NoError(t, err)

	// 7.5% of ₦6,500.00 is ₦487.50 -> 48750 kobo exactly.
	assert.Equal(t, int64(48750), totals.TaxKobo)
	assert.Equal(t, int64(698750), totals.TotalKobo)
}

// Every breakdown must satisfy the orders CHECK constraint, which is what turns
// an arithmetic mistake into a failed INSERT rather than an undercharge.
func TestTotalsAlwaysSatisfyTheOrdersCheckConstraint(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                               string
		subtotal, shipping, discount, rate int64
	}{
		{"plain", 500000, 150000, 0, 0},
		{"with vat", 500000, 150000, 0, 750},
		{"with discount", 500000, 150000, 100000, 750},
		{"free shipping", 1000000, 0, 0, 750},
		{"discount equals charge", 100000, 0, 107500, 750},
		{"awkward rounding", 333333, 77777, 0, 750},
		{"one kobo", 1, 0, 0, 750},
		{"zero everything", 0, 0, 0, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			totals, err := money.ComputeTotals(tc.subtotal, tc.shipping, tc.discount, money.BasisPoints(tc.rate))
			require.NoError(t, err)

			assert.Equal(t,
				totals.SubtotalKobo+totals.ShippingKobo+totals.TaxKobo-totals.DiscountKobo,
				totals.TotalKobo,
				"total must equal subtotal + shipping + tax - discount")
			assert.GreaterOrEqual(t, totals.TotalKobo, int64(0), "a total must never be negative")
		})
	}
}

// A discount larger than the charge would make the total negative, which the
// orders CHECK forbids -- and a negative total is a refund, not an order.
func TestDiscountCannotExceedTheCharge(t *testing.T) {
	t.Parallel()

	_, err := money.ComputeTotals(100000, 0, 200000, 0)
	require.ErrorIs(t, err, money.ErrDiscountExceedsCharge)
}

func TestNegativeComponentsAreRejected(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name                               string
		subtotal, shipping, discount, rate int64
	}{
		{"negative subtotal", -1, 0, 0, 0},
		{"negative shipping", 0, -1, 0, 0},
		{"negative discount", 0, 0, -1, 0},
		{"negative rate", 0, 0, 0, -1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := money.ComputeTotals(testCase.subtotal, testCase.shipping, testCase.discount, money.BasisPoints(testCase.rate))
			assert.ErrorIs(t, err, money.ErrNegativeAmount)
		})
	}
}

// Half-up, not banker's: it is what tax authorities expect and what a customer
// gets checking the arithmetic by hand.
func TestTaxRoundsHalfUp(t *testing.T) {
	t.Parallel()

	// 7.5% of 100 kobo is 7.5 -> 8.
	assert.Equal(t, int64(8), money.TaxOn(100, 750))
	// 7.5% of 300 kobo is 22.5 -> 23.
	assert.Equal(t, int64(23), money.TaxOn(300, 750))
	// 7.5% of 200 kobo is 15 exactly.
	assert.Equal(t, int64(15), money.TaxOn(200, 750))

	assert.Equal(t, int64(0), money.TaxOn(0, 750), "no base, no tax")
	assert.Equal(t, int64(0), money.TaxOn(100000, 0), "no rate, no tax")
}

// order_items carries CHECK (line_total_kobo = unit_price_kobo * quantity), so
// the application must compute it exactly as the database verifies it.
func TestLineTotal(t *testing.T) {
	t.Parallel()

	total, err := money.LineTotal(750000, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(2250000), total)

	_, err = money.LineTotal(750000, 0)
	require.Error(t, err, "a zero quantity line is meaningless")

	_, err = money.LineTotal(-1, 1)
	require.ErrorIs(t, err, money.ErrNegativeAmount)
}

// A free line (a zero-priced RSVP tier) is legitimate and must not be rejected.
func TestFreeLineIsAllowed(t *testing.T) {
	t.Parallel()

	total, err := money.LineTotal(0, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
}
