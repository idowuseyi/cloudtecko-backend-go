# Workflow Memory — verified, non-obvious learnings
<!-- Read this file at the start of every phase (right after state.md). -->
<!-- Update at every phase Handoff: APPEND new non-obvious learnings AND
     PRUNE — remove entries proven false or dismissed; remove or rewrite
     entries whose concern has been attended to; re-verify or drop anything
     no longer confirmable. This file holds ONLY what is CURRENTLY VERIFIED
     TRUE — a curated set, not a journal. History lives in git and the phase
     artifacts. -->
<!-- Never record what is derivable from the repo, the artifacts, or git
     history. If something derivable seems worth saving, keep only what was
     surprising or non-obvious about it. Record confirmations as well as
     corrections. NEVER store secrets or credentials here — the secret scan
     covers this directory too. -->
<!-- Entry format:
- <date> — <phase> — <the learning>.
  Why: <why it matters / what breaks without it>
  How to apply: <the concrete behavior next time>
-->

- 2026-09-08 — bootstrap — A full security audit already exists at
  `docs/security/2026-09-08-security-audit.md`, written in this workflow's
  finding format (S-NNN ... status).
  Why: the Verify phase must not redo it from scratch, and two findings (S-018
  plaintext access tokens, S-019 refresh tokens with no expiry) are deferred
  with an explicit flip condition — "before the first production deployment
  holding real user data" — which the GTM gate has to honour.
  How to apply: at Verify, lift those lines into `05-verify.md` and re-check
  only what has changed since.

- 2026-09-08 — bootstrap — `check-gate.sh` blocks on any open Critical/High
  finding, and S-018/S-019 are Medium *only* because no production data exists yet.
  Why: they become High the moment real users are onboarded, which would block
  the GTM gate rather than the Verify gate.
  How to apply: re-rate them at the GTM pre-flight instead of assuming the
  Verify-phase severity still holds.
