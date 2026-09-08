#!/usr/bin/env bash
# ship — machine gate check. Lives in a target project's .workflow/ trail and is
# copied there at bootstrap. It validates the current phase's artifact and, for
# code phases, runs the project's real build/test/lint. This is the enforcement
# a weak or stale model cannot fake: a red check blocks the gate.
#
# Exit 0 = gate may be presented / phase may advance.
# Exit 1 = blocked (quality problems printed above).
# Exit 2 = usage/setup error (no state.md, unknown phase).
set -u

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd -P)   # the .workflow/ directory
WF="$SCRIPT_DIR"
ROOT=$(dirname "$WF")                            # the project root
STATE="$WF/state.md"

fail=0
err()  { printf 'FAIL: %s\n' "$1"; fail=1; }
warn() { printf 'warn: %s\n' "$1"; }

[ -f "$STATE" ] || { echo "FAIL: no .workflow/state.md at $STATE"; exit 2; }

phase=$(sed -n 's/^- current_phase:[[:space:]]*\([A-Za-z0-9-]*\).*/\1/p' "$STATE" | head -n1)
[ -n "$phase" ] || { echo "FAIL: could not read current_phase from state.md"; exit 2; }
echo "Phase: $phase"

[ -f "$WF/memory.md" ] || warn "no .workflow/memory.md — copy templates/workflow/memory.md (Memory Protocol)"

# Print the body of a "## <header>" section, up to the next "## " header.
section_body() { # $1=file $2=header text
  awk -v h="## $2" '$0==h {g=1; next} /^## / {g=0} g {print}' "$1"
}

# Assert an artifact exists, carries all required headers, and has a real
# (non-comment, non-empty) ## Sources section.
validate_artifact() { # $1=artifact filename ; uses global $headers
  local a="$WF/$1"
  [ -f "$a" ] || { err "missing artifact $1"; return; }
  local h oldIFS="$IFS"
  IFS='|'
  for h in $headers; do
    grep -qF "## $h" "$a" || err "$1 missing section '## $h'"
  done
  IFS="$oldIFS"
  local src
  src=$(section_body "$a" "Sources" | grep -v '<!--' | grep -v '^[[:space:]]*$')
  [ -n "$src" ] || err "$1 has an empty ## Sources — cite or flag every external fact"
}

# Verify's ## Acceptance must map criteria to tests with at least one pass and no fail.
check_acceptance() {
  local body
  body=$(section_body "$WF/05-verify.md" "Acceptance" | grep -v '<!--')
  local passes fails blocked
  passes=$(printf '%s\n' "$body" | grep -Eci -- '(—|--|-)[[:space:]]*pass[[:space:]]*$' || true)
  fails=$(printf '%s\n' "$body" | grep -Eci -- '(—|--|-)[[:space:]]*fail[[:space:]]*$' || true)
  [ "${passes:-0}" -ge 1 ] || err "05-verify.md ## Acceptance lists no passing criterion"
  [ "${fails:-0}" -ge 1 ] && err "05-verify.md ## Acceptance still has failing criteria ($fails)"
  blocked=$(printf '%s\n' "$body" | grep -Eci -- '(—|--|-)[[:space:]]*blocked([[:space:]]*\(.*\))?[[:space:]]*$' || true)
  [ "${blocked:-0}" -ge 1 ] && err "05-verify.md ## Acceptance has blocked criteria ($blocked) — resolve the blocker or re-scope with the user"
}

# ## Security must be non-empty and carry no open Critical/High finding.
# Finding format (status LAST): S-NNN — title — severity — file:line — scenario — fix — status
check_security() {
  local body content open_high
  body=$(section_body "$WF/05-verify.md" "Security" | grep -v '<!--')
  content=$(printf '%s\n' "$body" | grep -v '^[[:space:]]*$')
  [ -n "$content" ] || { err "05-verify.md has an empty ## Security — run core/_security-review.md and record results"; return; }
  open_high=$(printf '%s\n' "$body" | grep -Ei -- '(—|--|-)[[:space:]]+(critical|high)[[:space:]]+(—|--|-)' | grep -Eci -- '(—|--|-)[[:space:]]*open[[:space:]]*$' || true)
  [ "${open_high:-0}" -ge 1 ] && err "05-verify.md ## Security has open Critical/High findings ($open_high) — fix them or record a user-approved deferred(reason + flip condition)"
}

# Secret scan of the working tree — including .workflow/ itself (agent-written
# artifacts leak secrets too). Working-tree scan, not git history, so
# pre-existing historical leaks don't permanently block adoption.
run_secret_scan() {
  cd "$ROOT" || return 0
  if command -v gitleaks >/dev/null 2>&1; then
    gitleaks detect --no-git --source . --redact >/dev/null 2>&1 || \
      err "secret scan (gitleaks) found leaks — run: gitleaks detect --no-git --source . --redact --verbose"
  else
    # Fallback: high-signal patterns only, to keep false positives near zero.
    local hits
    hits=$(grep -RInE \
      --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=vendor \
      --exclude-dir=dist --exclude-dir=build --exclude-dir=target \
      'sk_live_[0-9A-Za-z]{10,}|AKIA[0-9A-Z]{16}|-----BEGIN [A-Z ]*PRIVATE KEY|ghp_[A-Za-z0-9]{36}' \
      . 2>/dev/null | head -5)
    [ -n "$hits" ] && { printf '%s\n' "$hits" | cut -d: -f1,2; err "secret scan (grep fallback) found potential secrets at the locations above (values redacted) — remove/rotate them; install gitleaks for a fuller scan"; }
  fi
  cd "$WF" || true
}

# Detect the stack and run the real checks. $1=strict (1 = failures block).
run_code_checks() {
  local strict="$1" ran=0
  cd "$ROOT" || return 0
  if [ -f package.json ]; then
    ran=1
    local pm=npm
    [ -f pnpm-lock.yaml ] && pm=pnpm
    [ -f yarn.lock ] && pm=yarn
    if grep -q '"lint"' package.json; then
      "$pm" run lint || { [ "$strict" = 1 ] && err "lint failed" || warn "lint failed"; }
    fi
    if grep -q '"test"' package.json; then
      "$pm" test || { [ "$strict" = 1 ] && err "tests failed" || warn "tests failed"; }
    elif [ "$strict" = 1 ]; then
      err "no npm \"test\" script — Verify requires runnable automated tests"
    fi
  elif [ -f Cargo.toml ]; then
    ran=1; cargo test || { [ "$strict" = 1 ] && err "cargo test failed" || warn "cargo test failed"; }
  elif [ -f go.mod ]; then
    ran=1; go test ./... || { [ "$strict" = 1 ] && err "go test failed" || warn "go test failed"; }
  elif [ -f pyproject.toml ] || [ -f requirements.txt ] || [ -f setup.py ]; then
    ran=1
    if command -v pytest >/dev/null 2>&1; then
      pytest -q || { [ "$strict" = 1 ] && err "pytest failed" || warn "pytest failed"; }
    elif [ "$strict" = 1 ]; then
      err "pytest not available — Verify requires runnable tests"
    fi
  elif [ -f Makefile ] && grep -q '^test:' Makefile; then
    ran=1; make test || { [ "$strict" = 1 ] && err "make test failed" || warn "make test failed"; }
  fi
  [ "$ran" = 0 ] && [ "$strict" = 1 ] && \
    err "no recognized build/test system found, but this phase requires runnable tests"
  cd "$WF" || true
}

case "$phase" in
  01-discover) headers="Problem|Users & Context|Requirements|Success Criteria|Options Considered|Chosen Direction|Sources"
               validate_artifact 01-brief.md ;;
  02-plan)     headers="Work Breakdown|Architecture|Tech Choices|Sequence & Milestones|Risks|Sources"
               validate_artifact 02-plan.md ;;
  03-design)   headers="Components|Interfaces & Contracts|Data|UX / Flows|Error Handling|Sources"
               validate_artifact 03-design.md ;;
  04-implement) headers="Files Changed|Key Decisions|Deviations from Design|How to Run|Sources"
               validate_artifact 04-implementation.md
               run_code_checks 0 ;;
  05-verify)   headers="Test Coverage|Bugs Found|Fixes Applied|Residual Risks|Acceptance|Security|Not Covered|Sources"
               validate_artifact 05-verify.md
               check_acceptance
               check_security
               run_code_checks 1 ;;
  06-gtm)      if [ -f "$WF/06-gtm.md" ]; then
                 headers="Positioning|Audience & Channels|Messaging|Launch Assets|Sales / Next Steps|Sources"
                 validate_artifact 06-gtm.md
               else
                 echo "Pre-flight GTM gate: 06-gtm.md not written yet — validating Verify + code instead."
                 headers="Test Coverage|Bugs Found|Fixes Applied|Residual Risks|Acceptance|Security|Not Covered|Sources"
                 validate_artifact 05-verify.md
                 check_acceptance
                 check_security
               fi
               run_code_checks 1 ;;
  done)        echo "Workflow complete — re-checking build/tests."
               run_code_checks 1 ;;
  *)           echo "FAIL: unknown current_phase '$phase'"; exit 2 ;;
esac

run_secret_scan

if [ "$fail" -eq 0 ]; then
  echo "GATE CHECK PASSED"
  exit 0
else
  echo "GATE CHECK FAILED — resolve the FAIL lines above before presenting the gate."
  exit 1
fi
