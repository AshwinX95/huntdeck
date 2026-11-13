# Notifications

Settings › Notifications sends changes to **Discord**, **Telegram** or any
**webhook**. Add as many channels as you like; each picks its events, which
programs count, and when it's sent.

- **Events:** new private invites, new programs, scope added, programs
  reopened, bounties raised and platform problems (polls failing three times
  in a row, and when they work again) are on by default. Scope removed,
  out-of-scope changes, scope edits, programs paused or no longer listed,
  other bounty table changes and rules edits can be turned on. Programs that
  pause and reopen again and again (3+ times a week: Intigriti pauses at its
  report limit) aren't notified for each flip: a pause is sent once it has
  lasted a day, and the reopening after it.
- **Which programs:** platforms, paying or private only, a minimum top
  bounty, tags to include or exclude, and "only wildcards and new root
  domains" for new scope. Ignored programs never notify.
- **When:** after each poll (all of a poll's changes in one message, grouped
  by program), a daily digest at a time in your time zone, or both.
- **Delivery:** messages wait in the database and are retried with backoff
  (and after `Retry-After` when a service asks), so a restart or an outage
  doesn't lose or repeat them. A channel's address that's refused (wrong URL
  or token) is reported under Recent messages. "Send test" checks a channel,
  and the form's "Send a test" checks an address before you save it.
- **Preview:** a channel's form shows what it would have sent in the last
  week (messages, events, how many a day, and a few of them), and follows
  what you tick.
- **Links:** set HuntDeck's address (e.g. your tailnet name) and messages
  link to the program here; otherwise to the platform.

Setting up a channel:

- **Discord:** channel settings › Integrations › Webhooks › New Webhook ›
  Copy Webhook URL. Mentions are switched off, so a program named
  `@everyone` can't ping anyone.
- **Telegram:** message @BotFather, send `/newbot` and copy the token; send
  your new bot a message, and use your user ID (ask @userinfobot) as the chat
  ID, or a group's (negative) or `@channelname`.
- **Webhook:** any http(s) URL gets a JSON POST per message:

  ```json
  {"version": 1, "type": "poll", "sent_at": "2026-10-27T09:16:00Z",
   "events": [{"kind": "scope_added", "at": "…", "platform": "bugcrowd",
     "program": {"platform": "bugcrowd", "handle": "globex", "name": "Globex", "private": false,
       "bounty": true, "top_bounty": 15000, "currency": "USD", "hunt_score": 62, "tags": ["focus"],
       "url": "https://bugcrowd.com/globex", "huntdeck_url": "https://huntdeck.example/programs/bugcrowd/globex"},
     "assets": [{"identifier": "*.globex.example", "type": "wildcard", "in_scope": true, "bounty": true,
       "max_severity": "critical", "new_root": true}],
     "assets_total": 1}]}
  ```

  `type` is `poll`, `digest` or `test`; event kinds are `invite`, `launch`,
  `scope_added`, `scope_removed`, `scope_out`, `scope_changed`, `reopened`,
  `paused`, `removed`, `bounty_raised`, `rewards_changed`, `policy_changed`,
  `platform_problem`, `platform_recovered` and `test`, with `old`, `new`,
  `detail` and `change_id` where they apply. New fields may be added;
  existing ones keep their meaning. With a signing secret, each request
  carries `X-HuntDeck-Signature: sha256=<hex HMAC-SHA256 of the body>`.

Channel URLs and tokens are stored in the database (the same place as your
private program data) and are never shown again or logged.
