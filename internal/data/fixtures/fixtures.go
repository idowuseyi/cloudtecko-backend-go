package fixtures

import (
	"context"
	"fmt"
	"time"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
)

// Live Service fixtures to be applied by manually running the CLI "app db seed"
// Note that these fixtures are not available while testing
// see the separate internal/test/fixtures.go file

type Upsertable interface {
	Upsert(ctx context.Context, exec boil.ContextExecutor, updateOnConflict bool, conflictColumns []string, updateColumns, insertColumns boil.Columns, opts ...models.UpsertOptionFunc) error
}

// seedTimezone is the zone the seeded events are scheduled in.
const seedTimezone = "Africa/Lagos"

// Mind the declaration order! The fields get upserted exactly in the order they are declared.
//
// This mirrors the content the frontend currently hardcodes in app/events/page.tsx
// and app/resources/page.tsx, so those pages can drop their literal arrays and
// fetch instead without any visible change. Event dates are relative to seed time
// rather than the frontend's fixed 2025 dates -- a seeded event in the past would
// make "upcoming events" permanently empty.
type FixtureMap struct {
	CategoryWorkshop   *models.Category
	CategoryConference *models.Category
	CategoryMeetup     *models.Category
	CategoryWebinar    *models.Category
	CategoryBlog       *models.Category
	CategoryTemplate   *models.Category
	CategoryVideo      *models.Category

	EventAWSWorkshop  *models.Event
	EventAzureSummit  *models.Event
	EventGoogleDevFst *models.Event
	EventServerless   *models.Event
	EventCloudSec     *models.Event
	EventFinOps       *models.Event

	ResourceServerlessSec *models.Resource
	ResourceCICDTemplate  *models.Resource
	ResourceK8sTutorial   *models.Resource
	ResourceNextScaling   *models.Resource
	ResourceTailwind      *models.Resource
	ResourceVercelDeploy  *models.Resource
}

//nolint:funlen // a fixture builder is a long literal by nature
func Fixtures() FixtureMap {
	now := time.Now()
	fix := FixtureMap{}

	fix.CategoryWorkshop = category("a1000000-0000-4000-8000-000000000001", models.CategoryScopeEvent, "workshop", "Workshop", 0)
	fix.CategoryConference = category("a1000000-0000-4000-8000-000000000002", models.CategoryScopeEvent, "conference", "Conference", 1)
	fix.CategoryMeetup = category("a1000000-0000-4000-8000-000000000003", models.CategoryScopeEvent, "meetup", "Meetup", 2)
	fix.CategoryWebinar = category("a1000000-0000-4000-8000-000000000004", models.CategoryScopeEvent, "webinar", "Webinar", 3)
	fix.CategoryBlog = category("a1000000-0000-4000-8000-000000000005", models.CategoryScopeResource, "blog", "Blog", 0)
	fix.CategoryTemplate = category("a1000000-0000-4000-8000-000000000006", models.CategoryScopeResource, "template", "Template", 1)
	fix.CategoryVideo = category("a1000000-0000-4000-8000-000000000007", models.CategoryScopeResource, "video-tutorial", "Video Tutorial", 2)

	fix.EventAWSWorkshop = &models.Event{
		ID:           "b2000000-0000-4000-8000-000000000001",
		Slug:         "aws-certification-workshop-lagos",
		Title:        "AWS Certification Workshop: Lagos Edition",
		Summary:      "Hands-on training covering Cloud Practitioner and Solutions Architect associate tracks.",
		Body:         "Join industry leaders for insights on next-generation cloud architecture patterns, serverless computing, and enterprise-scale solutions.",
		CategoryID:   fix.CategoryWorkshop.ID,
		StartAt:      now.Add(14 * 24 * time.Hour),
		EndAt:        now.Add(14*24*time.Hour + 8*time.Hour),
		Timezone:     seedTimezone,
		VenueName:    null.StringFrom("Zone Tech Park"),
		VenueAddress: null.StringFrom("Gbagada, Lagos"),
		// The one featured event. A partial unique index enforces that only one
		// published row may carry this, so a second seeded featured event would
		// fail the seed rather than silently win.
		IsFeatured:  true,
		PublishedAt: null.TimeFrom(now),
	}

	fix.EventAzureSummit = onlineEvent(
		"b2000000-0000-4000-8000-000000000002",
		"azure-cloud-summit-2026",
		"Azure Cloud Summit 2026",
		"A virtual conference featuring Microsoft MVPs discussing enterprise-scale AI and DevOps.",
		fix.CategoryConference.ID,
		now.Add(20*24*time.Hour), 6*time.Hour, now,
	)

	fix.EventGoogleDevFst = &models.Event{
		ID:           "b2000000-0000-4000-8000-000000000003",
		Slug:         "google-cloud-devfest-abuja",
		Title:        "Google Cloud DevFest Abuja",
		Summary:      "Connect with local developers to explore Kubernetes, BigQuery, and Vertex AI patterns.",
		CategoryID:   fix.CategoryMeetup.ID,
		StartAt:      now.Add(35 * 24 * time.Hour),
		EndAt:        now.Add(35*24*time.Hour + 6*time.Hour),
		Timezone:     seedTimezone,
		VenueName:    null.StringFrom("Ventures Park"),
		VenueAddress: null.StringFrom("Wuse II, Abuja"),
		PublishedAt:  null.TimeFrom(now),
	}

	fix.EventServerless = onlineEvent(
		"b2000000-0000-4000-8000-000000000004",
		"serverless-architecture-in-nigeria",
		"Serverless Architecture in Nigeria",
		"Exploring cost-effective serverless solutions for startups using Lambda and DynamoDB.",
		fix.CategoryWebinar.ID,
		now.Add(42*24*time.Hour), 2*time.Hour, now,
	)

	fix.EventCloudSec = &models.Event{
		ID:           "b2000000-0000-4000-8000-000000000005",
		Slug:         "cloud-security-and-compliance-masterclass",
		Title:        "Cloud Security & Compliance Masterclass",
		Summary:      "Deep dive into IAM roles, VPC security groups, and protecting data at rest in the cloud.",
		CategoryID:   fix.CategoryWorkshop.ID,
		StartAt:      now.Add(54 * 24 * time.Hour),
		EndAt:        now.Add(54*24*time.Hour + 7*time.Hour),
		Timezone:     seedTimezone,
		VenueName:    null.StringFrom("Cloudadore HQ"),
		VenueAddress: null.StringFrom("Yaba, Lagos"),
		PublishedAt:  null.TimeFrom(now),
	}

	fix.EventFinOps = onlineEvent(
		"b2000000-0000-4000-8000-000000000006",
		"finops-africa-optimizing-cloud-spend",
		"FinOps Africa: Optimizing Cloud Spend",
		"Learn how to manage cloud budgets and reduce waste in multi-cloud environments.",
		fix.CategoryConference.ID,
		now.Add(63*24*time.Hour), 5*time.Hour, now,
	)

	fix.ResourceServerlessSec = resource(
		"c3000000-0000-4000-8000-000000000001",
		"serverless-security-protecting-your-functions",
		"Serverless Security: Protecting Your Functions",
		"Deep dive into serverless functions and secure deployments for modern cloud applications.",
		models.ResourceKindArticle, fix.CategoryBlog.ID, "Ada Obi", 8, now.Add(-20*24*time.Hour),
	)

	fix.ResourceCICDTemplate = resource(
		"c3000000-0000-4000-8000-000000000002",
		"ci-cd-pipeline-template-for-aws",
		"CI/CD Pipeline Template for AWS",
		"Use this prebuilt AWS CI/CD pipeline template for automated deployments and cloud efficiency.",
		models.ResourceKindDownload, fix.CategoryTemplate.ID, "Chidi Okonkwo", 5, now.Add(-18*24*time.Hour),
	)

	fix.ResourceK8sTutorial = resource(
		"c3000000-0000-4000-8000-000000000003",
		"kubernetes-deployment-zero-to-production",
		"Kubernetes Deployment Tutorial: Zero to Production",
		"Step-by-step video guide to deploying your first Kubernetes application.",
		models.ResourceKindVideo, fix.CategoryVideo.ID, "Bola Ade", 22, now.Add(-16*24*time.Hour),
	)

	fix.ResourceNextScaling = resource(
		"c3000000-0000-4000-8000-000000000004",
		"scaling-react-apps-with-nextjs",
		"Scaling React Apps with Next.js",
		"Learn best practices for scaling modern React apps using Next.js features and optimizations.",
		models.ResourceKindArticle, fix.CategoryBlog.ID, "Ada Obi", 11, now.Add(-14*24*time.Hour),
	)

	fix.ResourceTailwind = resource(
		"c3000000-0000-4000-8000-000000000005",
		"tailwind-landing-page-template",
		"Tailwind Landing Page Template",
		"A sleek, responsive landing page template built entirely with TailwindCSS.",
		models.ResourceKindDownload, fix.CategoryTemplate.ID, "Ngozi Eze", 4, now.Add(-12*24*time.Hour),
	)

	fix.ResourceVercelDeploy = resource(
		"c3000000-0000-4000-8000-000000000006",
		"deploying-with-vercel-step-by-step",
		"Deploying with Vercel: Step-by-Step Guide",
		"Master the process of deploying Next.js apps on Vercel with custom configurations.",
		models.ResourceKindVideo, fix.CategoryVideo.ID, "Bola Ade", 17, now.Add(-10*24*time.Hour),
	)

	return fix
}

func category(id string, scope string, slug string, name string, sortOrder int) *models.Category {
	return &models.Category{
		ID:        id,
		Scope:     scope,
		Slug:      slug,
		Name:      name,
		SortOrder: sortOrder,
	}
}

func onlineEvent(id string, slug string, title string, summary string, categoryID string, startAt time.Time, duration time.Duration, publishedAt time.Time) *models.Event {
	return &models.Event{
		ID:          id,
		Slug:        slug,
		Title:       title,
		Summary:     summary,
		CategoryID:  categoryID,
		StartAt:     startAt,
		EndAt:       startAt.Add(duration),
		Timezone:    seedTimezone,
		IsOnline:    true,
		MeetingURL:  null.StringFrom("https://meet.cloudadore.com/" + slug),
		PublishedAt: null.TimeFrom(publishedAt),
	}
}

func resource(id string, slug string, title string, summary string, kind string, categoryID string, author string, readingTime int, publishedAt time.Time) *models.Resource {
	return &models.Resource{
		ID:                 id,
		Slug:               slug,
		Title:              title,
		Summary:            summary,
		Kind:               kind,
		CategoryID:         categoryID,
		Author:             author,
		ReadingTimeMinutes: readingTime,
		PublishedAt:        null.TimeFrom(publishedAt),
	}
}

func Upserts() []Upsertable {
	fix := Fixtures()
	upsertableIfc := (*Upsertable)(nil)
	upserts, err := util.GetFieldsImplementing(&fix, upsertableIfc)
	if err != nil {
		panic(fmt.Errorf("failed to get upsertable fixture fields: %w", err))
	}

	return upserts
}
