// Package money does order arithmetic in integer minor units.
//
// Every amount here is kobo. There are no floats anywhere in this package, and
// none should be introduced: 0.1 + 0.2 is not 0.3, and a rounding error in a
// total is a rounding error in somebody's bank account.
package money

import (
	"errors"
	"fmt"
)

// BasisPoints expresses a tax rate as hundredths of a percent, so 7.5% VAT is
// 750. An integer rate keeps the whole calculation in integers.
type BasisPoints int64

const basisPointDivisor = 10000

var (
	// ErrNegativeAmount rejects a negative component outright. Every column
	// involved carries a >= 0 CHECK, so a negative would fail at the database
	// anyway -- catching it here produces a comprehensible error instead of a
	// constraint violation.
	ErrNegativeAmount = errors.New("money: amounts must not be negative")

	// ErrDiscountExceedsCharge rejects a discount larger than what is being
	// charged. Allowing it would make total_kobo negative, which the orders
	// CHECK forbids -- and a negative total is a refund, not an order.
	ErrDiscountExceedsCharge = errors.New("money: discount exceeds the amount charged")
)

// Totals is an order's money breakdown.
//
// The field relationship is NOT free-form: orders carries
//
//	CHECK (total_kobo = subtotal_kobo + shipping_kobo + tax_kobo - discount_kobo)
//
// so Total must always equal that sum. The constraint is what makes an
// arithmetic mistake a failed INSERT rather than a silent undercharge, and this
// package exists to compute a breakdown that satisfies it.
type Totals struct {
	SubtotalKobo int64
	ShippingKobo int64
	TaxKobo      int64
	DiscountKobo int64
	TotalKobo    int64
}

// ComputeTotals builds an order's breakdown with tax applied ON TOP of the
// taxable base.
//
// Tax-EXCLUSIVE, deliberately, because the orders CHECK adds tax_kobo into the
// total. Tax-inclusive pricing -- where the listed price already contains VAT --
// would mean tax_kobo is a portion OF subtotal rather than an addition to it,
// and the same sum would then double-count it. Switching to inclusive pricing is
// therefore a migration that changes that constraint, not a config flag; there
// is no way to support both under one CHECK.
//
// Tax applies to goods and shipping but never to a discount, and the discount is
// subtracted after tax. That ordering is a policy choice: it is the simplest one
// to explain on a receipt, and it does not let a discount reduce tax owed.
func ComputeTotals(subtotalKobo int64, shippingKobo int64, discountKobo int64, rate BasisPoints) (Totals, error) {
	if subtotalKobo < 0 || shippingKobo < 0 || discountKobo < 0 || rate < 0 {
		return Totals{}, ErrNegativeAmount
	}

	taxKobo := TaxOn(subtotalKobo+shippingKobo, rate)

	charged := subtotalKobo + shippingKobo + taxKobo
	if discountKobo > charged {
		return Totals{}, fmt.Errorf("%w: discount %d exceeds charge %d", ErrDiscountExceedsCharge, discountKobo, charged)
	}

	return Totals{
		SubtotalKobo: subtotalKobo,
		ShippingKobo: shippingKobo,
		TaxKobo:      taxKobo,
		DiscountKobo: discountKobo,
		TotalKobo:    charged - discountKobo,
	}, nil
}

// TaxOn returns the tax due on a base amount, rounded half-up to the nearest
// kobo.
//
// Half-up rather than banker's rounding: it is what tax authorities and
// accounting software conventionally expect, and it is what a customer gets if
// they check the arithmetic by hand. Computed entirely in integers -- the
// addition of half the divisor before dividing IS the rounding.
func TaxOn(baseKobo int64, rate BasisPoints) int64 {
	if baseKobo <= 0 || rate <= 0 {
		return 0
	}

	return (baseKobo*int64(rate) + basisPointDivisor/2) / basisPointDivisor
}

// LineTotal multiplies a unit price by a quantity.
//
// Separated out because order_items carries
// CHECK (line_total_kobo = unit_price_kobo * quantity), so the application must
// compute it exactly the way the database will verify it.
func LineTotal(unitPriceKobo int64, quantity int) (int64, error) {
	if unitPriceKobo < 0 {
		return 0, ErrNegativeAmount
	}

	if quantity <= 0 {
		return 0, fmt.Errorf("money: quantity must be positive, got %d", quantity)
	}

	return unitPriceKobo * int64(quantity), nil
}
