# cloudtecko-backend-go

Go API bootstrapped from allaboutapps/go-starter. See README.md and docs/.

## ship workflow

The `ship` toolkit lives at **`/home/dokimazo-tech/dev247/ship`**. The `/ship-*`
commands need it: they read `core/NN-<phase>.md` from that repo and execute it
against this project's `.workflow/`. Read `.workflow/state.md` first to find
`current_phase`, and obey the Gate Protocol in `core/_conventions.md`.

`.workflow/check-gate.sh` is the machine gate; it also blocks on any open
Critical/High security finding in `.workflow/05-verify.md`.

## Non-obvious things about this repo

- **`PROJECT_ROOT_DIR` is mandatory outside Docker.** `util.GetProjectRootDir()`
  defaults to the executable's directory, which is `<repo>/bin` after
  `make go-build`, so the app cannot find `migrations/`, `web/` or
  `api/swagger.yml`. The Makefile exports it; direct binary invocations must set it.
- **`.env.local` overrides real environment variables** (applied via `os.Setenv`).
  `docker-compose.yml` sets `DOTENV_DISABLE=true` so cloud credentials there
  cannot silently redirect the containerised app at a production database.
- **All generated code is committed** (`internal/models`, `internal/types`,
  `api/swagger.yml`, `handlers.go`, `wire_gen.go`), so `go build` needs no
  generation step. CI enforces this with `git diff --exit-code` after `make build`.
- **Postgres only.** The dialect seam in `internal/persistence` makes that
  explicit; see `docs/database-dialects.md` before assuming otherwise.
- **15 of 47 test packages need IntegreSQL**, which is not reachable from the
  host (it returns the compose-internal hostname `postgres`). Run those in the
  dev container.
- **Regenerating code needs the dev container** — go-swagger, sqlboiler,
  pg_format and spectral are pinned there, not on the host.

## Conventions

- `make lint` must be clean (golangci-lint is strict here: `mnd`, `varnamelen`,
  `wrapcheck`, `gocyclo` at 30 are all on).
- `gofmt` matters beyond style: `make build` runs `go-format` and CI then diffs
  the tree, so unformatted code fails the build.
