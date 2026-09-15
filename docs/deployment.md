# Deployment

Two targets, both built from a Dockerfile in this repository.

| | Render (primary) | Vercel (secondary) |
| --- | --- | --- |
| Config | `render.yaml` | `vercel.json` + `Dockerfile.vercel` |
| Build | `./Dockerfile`, `app` stage | `./Dockerfile.vercel`, self-contained |
| Process | long-lived container | Vercel Function, scales to zero |
| Migrations | `preDeployCommand` | run as a one-off |
| Health check | `/-/ready` | — |

Render is the production target because the process model matches: a long-lived
container with a real health check and a deploy hook to run migrations exactly
once. Vercel is suitable for previews and demos.

## Shared requirements

Both platforms inject `PORT`, and the app binds to it when
`SERVER_ECHO_LISTEN_ADDRESS` is unset. A service that ignores `PORT` never
becomes reachable and the platform reports only an opaque health-check timeout.

`/-/ready` is the health path on purpose: it is the single management route
whose mgmt-secret check is skipped, so a platform prober can reach it.
`/-/healthy` would return 400, since the platform cannot supply the secret.

## Environment

| Variable | Required | Notes |
| --- | --- | --- |
| `DATABASE_URL` | yes | Application traffic. Use the **pooled** endpoint if the provider has one. |
| `MIGRATION_DATABASE_URL` | if pooled | DDL. Use the **direct** endpoint. Falls back to `DATABASE_URL_UNPOOLED`, then `DATABASE_URL`. |
| `SERVER_MANAGEMENT_SECRET` | yes | Unset, a random secret is generated *per process*, so it differs between instances and changes on every restart. |
| `PROJECT_ROOT_DIR` | yes | `/app` in both images. |
| `SERVER_MANAGEMENT_PROBE_WRITEABLE_PATHS_ABS` | yes | `/tmp`. The container filesystem is otherwise not writable, and the liveness probe writes a touchfile. |
| `SERVER_ECHO_CORS_ALLOW_ORIGINS` | if browser clients | Comma-separated. Empty leaves CORS **off** — there is no wildcard fallback. |
| `SERVER_ECHO_ENABLE_PUBLIC_SWAGGER` | recommended | `false` in production. Both configs already set it. |
| `SERVER_ECHO_BASE_URL` | yes | Used in generated links and emails. |
| `SERVER_FRONTEND_BASE_URL` | yes | Password-reset links point here. |
| `DB_MAX_OPEN_CONNS` | recommended | Default is `NumCPU*2`, which overruns small managed plans. |
| `SERVER_LOGGER_LEVEL` | recommended | `info` in production; the default is `debug`. |
| `SERVER_AUTH_REGISTRATION_REQUIRES_CONFIRMATION` | **yes** | Must be `true`. With it false, `/register` answers a new address with a session and an existing one without, which discloses whether an address has an account (S-020). |
| `SERVER_PAYMENT_USE_MOCK` | no | Already defaults to `false`, unlike the push and storage mocks. Set it explicitly in production anyway, so the file can be audited without knowing the default. Turning it **on** in a reachable environment means anyone who can POST to `/webhooks/payments` can sign their own `charge.success` and be sent goods (S-034). |
| `SERVER_PAYMENT_SECRET_KEY` | if payments | Paystack secret key. Authenticates API calls **and** is the HMAC key webhooks are signed with — there is no separate webhook secret, so rotating it rotates webhook verification at the same instant. |
| `SERVER_PAYMENT_SECRET_ENCRYPTION_KEY` | if payments | Encrypts subscription email tokens. The service refuses to start without it when the mock provider is off. |
| `SERVER_PAYMENT_CALLBACK_URL` | if payments | Where the payer returns. A page only — fulfilment happens on the signed webhook. |
| `SERVER_STORAGE_USE_MOCK` | if uploads | `false` in production; mock storage is in-memory and lost on restart. |
| `SERVER_STORAGE_*` | if uploads | R2/S3 endpoint, keys, bucket. |
| `SERVER_STORAGE_PUBLIC_BASE_URL` | if uploads | **Must be a CDN or bucket custom domain, never this API's origin.** Serving user-supplied files same-origin turns any stored-content flaw into a same-origin scripting problem against the API. |

Never set `SERVER_PPROF_ENABLE=true` with
`SERVER_PPROF_ENABLE_MANAGEMENT_KEY_AUTH=false`. The app refuses to boot in that
combination outside debug, because it would publish heap and goroutine dumps —
which routinely contain tokens and connection strings — unauthenticated.

## Render

`render.yaml` is a blueprint: point Render at the repository and it picks it up.
Every secret is marked `sync: false`, so Render prompts for it rather than
storing it in the file.

Migrations run through `preDeployCommand: /app/app db migrate`, once per deploy,
before the new instance takes traffic. The image's default `CMD` is
`server --migrate`; `render.yaml` deliberately overrides it with
`dockerCommand: /app/app server`, because with more than one instance every
replica would otherwise race to apply the same migrations at boot.

## Vercel

```json
{
  "services": { "api": { "runtime": "container", "entrypoint": "Dockerfile.vercel" } },
  "rewrites": [ { "source": "/(.*)", "destination": { "service": "api" } } ]
}
```

The catch-all rewrite is required — without it the service returns 404.

`Dockerfile.vercel` is self-contained rather than deriving from `./Dockerfile`,
because Vercel builds it on its own builder and cannot reference an image
produced elsewhere. It skips the entire development toolchain, which is only
needed to regenerate code, and produces an ~18 MB image.

Constraints that shaped it:

- **Stateless, ephemeral filesystem.** Nothing may be written outside `/tmp`.
- **Scales to zero**, so cold starts include opening a database connection.
  `DB_PING_ON_CONNECT=false` skips the connect-time verification if that proves
  costly.
- **1,024 file descriptors shared across all concurrent executions** — hence
  `DB_MAX_OPEN_CONNS=5`. A large pool is actively harmful here.
- **4.5 MB request body cap** at the platform level, below the app's own limit.
- **No background work.** This service has no scheduler or workers, so nothing
  is lost; do not add any without revisiting this.

Migrations are **not** run on boot: a scale-to-zero platform can start several
instances simultaneously and they would race. Run them as a one-off:

```sh
docker run --rm -e DATABASE_URL=... <image> db migrate
```

With Vercel's Neon integration, `DATABASE_URL_UNPOOLED` is injected
automatically and is picked up for migrations without further configuration.

## Verifying a deployment

```sh
curl -i https://<host>/-/ready                              # 200
curl -i "https://<host>/-/healthy?mgmt-secret=$SECRET"      # 200
curl -i https://<host>/swagger.yml                          # 404 when disabled
curl -i https://<host>/metrics                              # 404 — never public
```

`GET /-/version` (with the secret) returns the module name, commit and build
date, which is the quickest way to confirm which revision is live.

## Other platforms

The `app` stage is an ordinary distroless image running as uid 65532, so
Fly.io, Railway, Cloud Run and plain Kubernetes all work with the same
environment. Only the health-check path and the migration hook need wiring.


## Payments

The Paystack secret key does double duty: it authenticates outbound API calls and
is the HMAC key Paystack signs webhook bodies with. There is no separate webhook
secret, so rotating the key rotates webhook verification at the same moment —
plan for a brief window where in-flight deliveries signed with the old key will
fail verification and be retried.

Register the webhook URL as `https://<your-host>/webhooks/payments` and confirm a
signed delivery reaches it **before** switching to live keys. The endpoint is
deliberately not rate-limited: throttling a provider's retries would drop money
events, and the signature is what makes it safe to expose.

Two properties to be aware of operationally:

- **Paystack's signature carries no timestamp**, so a captured delivery verifies
  forever. Replay protection is entirely server-side, via a unique idempotency
  key plus guarded state transitions. Nothing to configure, but it means webhook
  URL secrecy buys you nothing — do not treat it as a secret.
- **Retries run for up to 72 hours** (every 3 minutes for four attempts, then
  hourly). A non-2xx is therefore a request to be retried, which is correct for a
  genuine failure and wrong for anything we will never handle — the handler
  returns 200 for authentic-but-unsupported events for that reason.

`GET /api/v1/admin/webhook-events` lists deliveries that were received and never
processed. It should always be empty; an entry means a provider reported a
payment and we failed to act on it. Alert on it.

## Scheduled work

There is no job runner in the application. Two things need a scheduler, and both
are cobra subcommands so a platform cron can invoke them:

| Command | Cadence | Why |
| --- | --- | --- |
| `app db purge-expired` | daily | Hard-deletes contact messages and membership applications past their retention window. Nothing about serving a request depends on expired personal data being gone, so unlike a reservation hold there is no lazy path that would trigger it. |
| `app db grant-scope` | on demand | Not scheduled. This is how the FIRST admin is created: `app db grant-scope --username <email> --scope admin`. There is deliberately no environment variable that auto-grants admin at boot, because that turns a leaked env file into privilege escalation and leaves no audit trail. |

Reservation expiry does **not** need a cron: it runs opportunistically at the top
of every checkout, because contention on a tier is exactly when expiry matters.
