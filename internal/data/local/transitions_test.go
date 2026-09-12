package local_test

import (
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/local"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/stretchr/testify/assert"
)

// The transition helper interpolates a table name into SQL, so the name is a
// constant rather than a value from models.TableNames (which is a var and cannot
// initialise a const). This keeps the two in step: renaming a table then fails
// here in CI rather than at runtime, inside a payment.
func TestTransitionTableNamesMatchGeneratedModels(t *testing.T) {
	t.Parallel()

	names := local.TransitionTableNamesForTest()

	assert.Equal(t, models.TableNames.Payments, names["payments"])
	assert.Equal(t, models.TableNames.Orders, names["orders"])
}
