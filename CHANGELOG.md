# Changelog

All notable changes to HuntDeck. Versions follow [Semantic Versioning](https://semver.org/):
the API under `/api/v1` and `/api/v2`, the configuration variables and the
database (upgraded in place on start) stay compatible within a major
version.

## v1.0.0 (2026-11-13)

The first public release.

### Platforms

- HackerOne (official Hacker API, plus its public program pages for stats;
  optional website login for private programs' stats and invitations),
  Bugcrowd, Intigriti and YesWeHack, each with your own credentials,
  **private invites included**. Logins with TOTP run in a built-in browser
  where a platform needs one; sessions are kept across restarts.
- Programs added by hand (`independent`), and custom sources: a script of
  yours writes any other platform's programs and scope as JSON.
- Polite polling: per-platform rate limits, `Retry-After`, bounded retries,
  quick polls every 15 minutes and full ones every 6 hours (editable in
  Settings), and a removal is only ever recorded from a complete listing.

### Tracking

- Scope and its history: what was added, removed or edited and when (dated
  from the platforms' own scope history where they have one), rules and
  bounty table changes with word-level diffs, status changes, announcements.
- Public program stats on every platform: bounties paid, reports received
  and valid in 90 days, average bounty, response times, hunters and
  leaderboards, Hacktivity and CrowdStream reports, with daily history.
  Estimates are marked as such; the Methodology page explains each number.
- HuntScore: which programs are best to hunt now, with its reasons.

### Web UI

- Hunt board (fresh attack surface, the week's scope changes, top picks,
  invites), Programs and Invites with a filter rail, views and column
  picker, program pages (overview, scope, policy, stats, changes, notes),
  Targets, Changes, Disclosed, Stats, the scope check, and Settings.
- Kept in the browser: a watchlist, notes on programs, named saved views,
  "new since your last visit" marks. Keyboard shortcuts (`?` lists them).
- Light and dark themes in three palettes; phone-friendly cards.

### Exports and API

- Plain-text target lists for recon tools, Burp, ZAP and Caido scope files,
  CSV, Atom feeds of changes, a bulk scope check for tool output.
- A REST API described by OpenAPI 3.1 (`/api/v1/openapi.json`) and an API
  page that sends requests for you. `/api/v2` adds, for each number, whether
  it's an estimate, where it comes from and when it last changed; program
  lists and pages, invites and `GET /api/v1/match` answer conditional
  requests (ETag, 304); `since=` lists only what changed.

### Notifications

- Discord, Telegram and webhooks, with rules per channel (event kinds,
  platforms, bounty and scope filters, tags), instant or as a daily digest,
  through an outbox that retries.

### Running it

- One container (Docker Compose or `docker run`), SQLite inside, a
  read-only root filesystem, non-root. The published image is linux/amd64;
  on arm64 (Apple Silicon, Raspberry Pi, ARM servers) build it with
  `docker compose up -d --build`.
- A new install loads the public starter seed on its first start, once, so
  it begins with months of history for the public programs
  (`HUNTDECK_SEED_URL=off` to start empty).
