package fixtures

import (
	"context"
	"fmt"
	"time"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/auth"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/hashing"
)

const (
	// 18 characters: registration now requires at least 15 (NIST SP 800-63B Rev 4,
	// which mandates 15 when a password is the only authentication factor).
	// HashedTestUserPassword is the argon2id hash OF PlainTestUserPassword -- change
	// one and you must regenerate the other, or every fixture login breaks.
	PlainTestUserPassword  = "test-user-password"
	HashedTestUserPassword = "$argon2id$v=19$m=65536,t=1,p=4$R2X4C7x4Eg6k9TVuPoUYiA$Dqhstn1WyYrqWyHUJePTablDOJkKY512yhscqx92Rtw" //nolint:gosec
)

// Every credential-bearing token (access, refresh, password-reset, confirmation)
// is stored as a hash, so the plaintext lives here and only
// hashing.HashToken(...) of it reaches the database. Tests authenticate with
// these values; the database never contains them.
//
//nolint:gosec // static test fixtures for the throwaway test database
const (
	User1AccessToken1Plain            = "1cfc27d7-a178-4051-802b-f3ff3967c95c"
	User1RefreshToken1Plain           = "66412eaf-2b89-404d-bbb5-46c3b8bf1a53"
	User2AccessToken1Plain            = "115d28c5-f585-4fb5-9656-fb321739fee5"
	User2RefreshToken1Plain           = "ea909c75-63d1-4348-a63c-4bcf8ab334a2"
	UserDeactivatedAccessToken1Plain  = "24d0b38d-387c-400c-80fc-a71d85031d4c"
	UserDeactivatedRefreshToken1Plain = "b6e13a88-7b18-4f17-b819-71b196be2444"

	UserRequiresConfirmationConfirmationTokenPlain = "c9182e0b-4a46-4825-9f10-56f04a8b1665"
)

// fixtureTimezone is the IANA zone every fixture event is scheduled in.
const fixtureTimezone = "Africa/Lagos"

// Insertable represents a common interface for all model instances so they may be inserted via the Inserts() func
type Insertable interface {
	Insert(ctx context.Context, exec boil.ContextExecutor, columns boil.Columns) error
}

// The main definition which fixtures are available through Fixtures().
// Mind the declaration order! The fields get inserted exactly in the order they are declared.
type FixtureMap struct {
	User1                                     *models.User
	User1AppUserProfile                       *models.AppUserProfile
	User1AccessToken1                         *models.AccessToken
	User1RefreshToken1                        *models.RefreshToken
	User2                                     *models.User
	User2AppUserProfile                       *models.AppUserProfile
	User2AccessToken1                         *models.AccessToken
	User2RefreshToken1                        *models.RefreshToken
	UserDeactivated                           *models.User
	UserDeactivatedAppUserProfile             *models.AppUserProfile
	UserDeactivatedAccessToken1               *models.AccessToken
	UserDeactivatedRefreshToken1              *models.RefreshToken
	User1PushToken                            *models.PushToken
	User1PushTokenAPN                         *models.PushToken
	UserRequiresConfirmation                  *models.User
	UserRequiresConfirmationAppUserProfile    *models.AppUserProfile
	UserRequiresConfirmationConfirmationToken *models.ConfirmationToken

	// Content fixtures. FK order matters and is enforced by declaration order:
	// categories and media assets must exist before the events and resources
	// that reference them.
	CategoryEventWorkshop    *models.Category
	CategoryEventConference  *models.Category
	CategoryResourceBlog     *models.Category
	CategoryResourceTemplate *models.Category
	MediaHeroReady           *models.MediaAsset
	EventFeatured            *models.Event
	EventPublished           *models.Event
	EventDraft               *models.Event
	EventArchived            *models.Event
	ResourceServerless       *models.Resource
	ResourceKubernetes       *models.Resource
	ResourceDraft            *models.Resource
}

// Fixtures returns a function wrapping our fixtures, which tests are allowed to manipulate.
// Each test (which may run concurrently) receives a fresh copy, preventing side effects between test runs.
func Fixtures() FixtureMap {
	now := time.Now()
	f := FixtureMap{}

	f.User1 = &models.User{
		ID:       "f6ede5d8-e22a-4ca5-aa12-67821865a3e5",
		IsActive: true,
		Username: null.StringFrom("user1@example.com"),
		Password: null.StringFrom(HashedTestUserPassword),
		Scopes:   []string{auth.ScopeApp.String()},
	}

	f.User1AppUserProfile = &models.AppUserProfile{
		UserID:          f.User1.ID,
		LegalAcceptedAt: null.TimeFrom(now.Add(time.Minute * -10)),
	}

	f.User1AccessToken1 = &models.AccessToken{
		TokenHash:  hashing.HashToken(User1AccessToken1Plain),
		ValidUntil: now.Add(10 * 365 * 24 * time.Hour),
		UserID:     f.User1.ID,
	}

	f.User1RefreshToken1 = &models.RefreshToken{
		TokenHash:  hashing.HashToken(User1RefreshToken1Plain),
		ValidUntil: now.Add(10 * 365 * 24 * time.Hour),
		UserID:     f.User1.ID,
	}

	f.User2 = &models.User{
		ID:       "76a79a2b-fbd8-45a0-b35b-671a28a87acf",
		IsActive: true,
		Username: null.StringFrom("user2@example.com"),
		Password: null.StringFrom(HashedTestUserPassword),
		Scopes:   []string{auth.ScopeApp.String()},
	}

	f.User2AppUserProfile = &models.AppUserProfile{
		UserID:          f.User2.ID,
		LegalAcceptedAt: null.TimeFrom(now.Add(time.Minute * -10)),
	}

	f.User2AccessToken1 = &models.AccessToken{
		TokenHash:  hashing.HashToken(User2AccessToken1Plain),
		ValidUntil: now.Add(10 * 365 * 24 * time.Hour),
		UserID:     f.User2.ID,
	}

	f.User2RefreshToken1 = &models.RefreshToken{
		TokenHash:  hashing.HashToken(User2RefreshToken1Plain),
		ValidUntil: now.Add(10 * 365 * 24 * time.Hour),
		UserID:     f.User2.ID,
	}

	f.UserRequiresConfirmation = &models.User{
		ID:                   "402e1669-fff7-43ca-8f08-071c06409479",
		IsActive:             false,
		RequiresConfirmation: true,
		Username:             null.StringFrom("userrequiresconfirmation@example.com"),
		Password:             null.StringFrom(HashedTestUserPassword),
		Scopes:               []string{auth.ScopeApp.String()},
	}

	f.UserRequiresConfirmationAppUserProfile = &models.AppUserProfile{
		UserID:          f.UserRequiresConfirmation.ID,
		LegalAcceptedAt: null.TimeFrom(now.Add(time.Minute * -10)),
	}

	f.UserRequiresConfirmationConfirmationToken = &models.ConfirmationToken{
		TokenHash:  hashing.HashToken(UserRequiresConfirmationConfirmationTokenPlain),
		ValidUntil: now.Add(time.Hour),
		UserID:     f.UserRequiresConfirmation.ID,
	}

	f.UserDeactivated = &models.User{
		ID:       "d9c0dee9-239e-4323-979a-a5354d289627",
		IsActive: false,
		Username: null.StringFrom("userdeactivated@example.com"),
		Password: null.StringFrom(HashedTestUserPassword),
		Scopes:   []string{auth.ScopeApp.String()},
	}

	f.UserDeactivatedAppUserProfile = &models.AppUserProfile{
		UserID:          f.UserDeactivated.ID,
		LegalAcceptedAt: null.Time{},
	}

	f.UserDeactivatedAccessToken1 = &models.AccessToken{
		TokenHash:  hashing.HashToken(UserDeactivatedAccessToken1Plain),
		ValidUntil: now.Add(10 * 365 * 24 * time.Hour),
		UserID:     f.UserDeactivated.ID,
	}

	f.UserDeactivatedRefreshToken1 = &models.RefreshToken{
		TokenHash:  hashing.HashToken(UserDeactivatedRefreshToken1Plain),
		ValidUntil: now.Add(10 * 365 * 24 * time.Hour),
		UserID:     f.UserDeactivated.ID,
	}

	f.User1PushToken = &models.PushToken{
		ID:       "98ad176b-af90-44b7-b991-d9ebfc5dd9a0",
		Token:    "cQ_Qk3ZCCZelUZ_K_Yn2BV:APA91bG4jst5srGYZqBAn_wRfiJUzAOQ4k8tV0sDcV4uas2ln5wNwkE_ebneR5Fqk7GvndZ-h3mWnjWaI8yZ4sVwo8qu_Aztotqup4mlEPNYgFGqTlJ5ltQrJG5oKp4RoYQ_0CeFaymn",
		UserID:   f.User1.ID,
		Provider: models.ProviderTypeFCM,
	}

	f.User1PushTokenAPN = &models.PushToken{
		ID:       "5909b472-86f8-4d15-bb63-d49f4fad41a3",
		Token:    "0a863a72-d391-4217-9f26-388801684744",
		UserID:   f.User1.ID,
		Provider: models.ProviderTypeApn,
	}

	contentFixtures(&f, now)

	return f
}

// Inserts defines the order in which the fixtures will be inserted
// into the test database
//
//nolint:funlen // a fixture builder is a long literal by nature
func contentFixtures(fix *FixtureMap, now time.Time) {
	fix.CategoryEventWorkshop = &models.Category{
		ID:    "b0a2f0de-0d2e-4a1e-9a3c-2f1b7c8d4e01",
		Scope: models.CategoryScopeEvent,
		Slug:  "workshop",
		Name:  "Workshop",
	}

	fix.CategoryEventConference = &models.Category{
		ID:        "b0a2f0de-0d2e-4a1e-9a3c-2f1b7c8d4e02",
		Scope:     models.CategoryScopeEvent,
		Slug:      "conference",
		Name:      "Conference",
		SortOrder: 1,
	}

	fix.CategoryResourceBlog = &models.Category{
		ID:    "b0a2f0de-0d2e-4a1e-9a3c-2f1b7c8d4e03",
		Scope: models.CategoryScopeResource,
		Slug:  "blog",
		Name:  "Blog",
	}

	fix.CategoryResourceTemplate = &models.Category{
		ID:        "b0a2f0de-0d2e-4a1e-9a3c-2f1b7c8d4e04",
		Scope:     models.CategoryScopeResource,
		Slug:      "template",
		Name:      "Template",
		SortOrder: 1,
	}

	fix.MediaHeroReady = &models.MediaAsset{
		ID:          "c1b3f1ef-1e3f-4b2f-8b4d-3a2c8d9e5f01",
		ObjectKey:   "public/events/aws-workshop.jpg",
		Bucket:      "cloudadore-media",
		ContentType: "image/jpeg",
		ByteSize:    204800,
		Status:      models.MediaStatusReady,
		PublicURL:   null.StringFrom("https://media.cloudadore.test/events/aws-workshop.jpg"),
	}

	fix.EventFeatured = &models.Event{
		ID:           "d2c4f2f0-2f40-4c30-9c5e-4b3d9eaf6001",
		Slug:         "aws-certification-workshop-lagos",
		Title:        "AWS Certification Workshop: Lagos Edition",
		Summary:      "Hands-on training covering Cloud Practitioner and Solutions Architect associate tracks.",
		Body:         "A full day of labs.",
		CategoryID:   fix.CategoryEventWorkshop.ID,
		HeroMediaID:  null.StringFrom(fix.MediaHeroReady.ID),
		StartAt:      now.Add(14 * 24 * time.Hour),
		EndAt:        now.Add(14*24*time.Hour + 8*time.Hour),
		Timezone:     fixtureTimezone,
		VenueName:    null.StringFrom("Zone Tech Park"),
		VenueAddress: null.StringFrom("Gbagada, Lagos"),
		IsFeatured:   true,
		PublishedAt:  null.TimeFrom(now.Add(-24 * time.Hour)),
	}

	fix.EventPublished = &models.Event{
		ID:          "d2c4f2f0-2f40-4c30-9c5e-4b3d9eaf6002",
		Slug:        "azure-cloud-summit-2026",
		Title:       "Azure Cloud Summit 2026",
		Summary:     "A virtual conference featuring Microsoft MVPs discussing enterprise-scale AI and DevOps.",
		CategoryID:  fix.CategoryEventConference.ID,
		StartAt:     now.Add(30 * 24 * time.Hour),
		EndAt:       now.Add(30*24*time.Hour + 4*time.Hour),
		Timezone:    fixtureTimezone,
		IsOnline:    true,
		MeetingURL:  null.StringFrom("https://meet.cloudadore.test/azure-summit"),
		PublishedAt: null.TimeFrom(now.Add(-48 * time.Hour)),
	}

	// Never published: must not appear in any public listing or detail response.
	fix.EventDraft = &models.Event{
		ID:         "d2c4f2f0-2f40-4c30-9c5e-4b3d9eaf6003",
		Slug:       "unpublished-draft-event",
		Title:      "Unpublished Draft Event",
		Summary:    "Should never be visible to the public.",
		CategoryID: fix.CategoryEventWorkshop.ID,
		StartAt:    now.Add(60 * 24 * time.Hour),
		EndAt:      now.Add(60*24*time.Hour + 2*time.Hour),
		Timezone:   fixtureTimezone,
	}

	// Published once, then retired: also must not appear.
	fix.EventArchived = &models.Event{
		ID:          "d2c4f2f0-2f40-4c30-9c5e-4b3d9eaf6004",
		Slug:        "archived-past-event",
		Title:       "Archived Past Event",
		Summary:     "Retired after it happened.",
		CategoryID:  fix.CategoryEventWorkshop.ID,
		StartAt:     now.Add(-60 * 24 * time.Hour),
		EndAt:       now.Add(-60*24*time.Hour + 2*time.Hour),
		Timezone:    fixtureTimezone,
		PublishedAt: null.TimeFrom(now.Add(-90 * 24 * time.Hour)),
		ArchivedAt:  null.TimeFrom(now.Add(-30 * 24 * time.Hour)),
	}

	fix.ResourceServerless = &models.Resource{
		ID:                 "e3d5f3f1-3f51-4d41-8d6f-5c4eafb07001",
		Slug:               "serverless-security-protecting-your-functions",
		Title:              "Serverless Security: Protecting Your Functions",
		Summary:            "Deep dive into serverless functions and secure deployments for modern cloud applications.",
		Body:               "Lambda hardening, IAM least privilege and secrets handling.",
		Kind:               models.ResourceKindArticle,
		CategoryID:         fix.CategoryResourceBlog.ID,
		Author:             "Ada Obi",
		ReadingTimeMinutes: 8,
		PublishedAt:        null.TimeFrom(now.Add(-72 * time.Hour)),
	}

	fix.ResourceKubernetes = &models.Resource{
		ID:                 "e3d5f3f1-3f51-4d41-8d6f-5c4eafb07002",
		Slug:               "kubernetes-deployment-zero-to-production",
		Title:              "Kubernetes Deployment Tutorial: Zero to Production",
		Summary:            "Step-by-step guide to deploying your first Kubernetes application.",
		Body:               "Mentions serverless once, in passing, so relevance ranking has something to separate.",
		Kind:               models.ResourceKindVideo,
		CategoryID:         fix.CategoryResourceTemplate.ID,
		Author:             "Bola Ade",
		ReadingTimeMinutes: 15,
		PublishedAt:        null.TimeFrom(now.Add(-96 * time.Hour)),
	}

	fix.ResourceDraft = &models.Resource{
		ID:         "e3d5f3f1-3f51-4d41-8d6f-5c4eafb07003",
		Slug:       "unpublished-draft-resource",
		Title:      "Unpublished Draft Resource",
		Summary:    "Should never be visible to the public.",
		Kind:       models.ResourceKindArticle,
		CategoryID: fix.CategoryResourceBlog.ID,
		Author:     "Nobody",
	}
}

func Inserts() []Insertable {
	fix := Fixtures()
	insertableIfc := (*Insertable)(nil)
	inserts, err := util.GetFieldsImplementing(&fix, insertableIfc)
	if err != nil {
		panic(fmt.Errorf("failed to get insertable fixture fields: %w", err))
	}

	return inserts
}
