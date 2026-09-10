package db_test

import (
	"database/sql"
	"sort"
	"strings"
	"testing"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/test/fixtures"
	swaggerTypes "github.com/idowuseyi/cloudtecko-backend-go/internal/types"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrderBy(t *testing.T) {
	test.WithTestDatabase(t, func(sqlDB *sql.DB) {
		ctx := t.Context()
		fix := fixtures.Fixtures()

		_, err := fix.UserRequiresConfirmation.Delete(ctx, sqlDB)
		require.NoError(t, err)

		noUsername := models.User{
			Scopes: types.StringArray{"cms"},
		}

		upperUsername := models.User{
			Username: null.StringFrom("USER3@example.com"),
			Scopes:   types.StringArray{"cms"},
		}

		err = noUsername.Insert(ctx, sqlDB, boil.Infer())
		require.NoError(t, err)

		err = upperUsername.Insert(ctx, sqlDB, boil.Infer())
		require.NoError(t, err)

		users, err := models.Users(db.OrderBy(swaggerTypes.OrderDirAsc, models.TableNames.Users, models.UserColumns.Username)).All(ctx, sqlDB)
		require.NoError(t, err)
		require.NotEmpty(t, users)
		assert.Equal(t, upperUsername.ID, users[0].ID)
		assert.Equal(t, upperUsername.Username, users[0].Username)

		// OrderByLower is case-insensitive, so the uppercase username must NOT lead
		// the way it does under the plain OrderBy asserted above. Assert that
		// property plus overall sortedness rather than naming a specific fixture:
		// "user1 is alphabetically first" was only incidentally true, and broke as
		// soon as a fixture sorting ahead of it was added.
		users, err = models.Users(db.OrderByLower(swaggerTypes.OrderDirAsc, models.TableNames.Users, models.UserColumns.Username)).All(ctx, sqlDB)
		require.NoError(t, err)
		require.NotEmpty(t, users)
		assert.NotEqual(t, upperUsername.ID, users[0].ID, "lower-cased ordering must not put USER3 first")

		// Compare only the non-NULL usernames: PostgreSQL's ASC implies NULLS
		// LAST, so the NULL-username row trails the result -- but its Go zero
		// value is "", which would read as sorting first.
		named := make([]string, 0, len(users))

		for _, u := range users {
			if u.Username.Valid {
				named = append(named, strings.ToLower(u.Username.String))
			}
		}

		assert.True(t, sort.StringsAreSorted(named),
			"usernames must be ordered case-insensitively ascending, got %v", named)

		users, err = models.Users(db.OrderByWithNulls(swaggerTypes.OrderDirAsc, db.OrderByNullsFirst, models.TableNames.Users, models.UserColumns.Username)).All(ctx, sqlDB)
		require.NoError(t, err)
		require.NotEmpty(t, users)
		assert.Equal(t, noUsername.ID, users[0].ID)
		assert.Equal(t, noUsername.Username, users[0].Username)

		users, err = models.Users(db.OrderByLowerWithNulls(swaggerTypes.OrderDirDesc, db.OrderByNullsLast, models.TableNames.Users, models.UserColumns.Username)).All(ctx, sqlDB)
		require.NoError(t, err)
		require.NotEmpty(t, users)
		assert.Equal(t, fix.UserDeactivated.ID, users[0].ID)
		assert.Equal(t, fix.UserDeactivated.Username, users[0].Username)
		assert.Equal(t, upperUsername.ID, users[1].ID)
		assert.Equal(t, upperUsername.Username, users[1].Username)
	})
}
