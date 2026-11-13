# Security

## Reporting a vulnerability

Use the repository's private security advisory ("Report a vulnerability"
under the Security tab). Please don't open a public issue for a
vulnerability.

Describe the problem with HuntDeck itself, with steps to reproduce it on a
fresh install. Use made-up data only: never send

- your HuntDeck database, seeds exported from it, or logs;
- private program names, scope, briefs or anything under a platform's NDA;
- platform credentials, session cookies, API tokens, TOTP secrets or
  notification webhook URLs;
- details of a vulnerability you reported to a bug bounty program.

You'll get an answer within a week. Fixes go into the next release, and the
advisory is published once a release with the fix is out.

## Supported versions

The latest release. HuntDeck upgrades its database by itself on start (with
a backup first), so staying current is a matter of pulling the new image.

## Security model

HuntDeck is a personal tool, and its design assumes it is reached only by
you:

- **There is no login, by design.** It listens on `127.0.0.1` by default;
  reach it from elsewhere through a private network such as Tailscale or a
  VPN, never by publishing the port to the internet.
- The `Host` header is checked against IPs, `localhost` and
  `HUNTDECK_ALLOWED_HOSTS`, so a website you visit can't reach it by DNS
  rebinding; cross-site form posts and API calls are refused.
- Pages are served with a strict Content-Security-Policy (`'self'` only),
  `frame-ancestors 'none'` and `Referrer-Policy: no-referrer`.
- The container runs as a non-root user with a read-only root filesystem
  and no capabilities.
- Credentials come from the environment, `*_FILE` secrets or Settings, where
  saved values are encrypted at rest; they're never logged or shown again.
- Nothing is sent anywhere but the platforms you configure, the notification
  channels you add, and a new install's one download of the public starter
  seed (`HUNTDECK_SEED_URL=off` turns it off).

So "anyone who can reach the port can use it" is expected behaviour, not a
vulnerability. What is in scope: anything that lets a website, a platform's
data (program names, scope, policy text), a seed or a custom source run
code, read or change your data, or reach HuntDeck despite the checks above;
credentials or private program data leaking into logs, exports, seeds or
the read-only demo mode; and anything that makes HuntDeck break a
platform's rate limits.
