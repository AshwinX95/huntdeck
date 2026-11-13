-- Notifications (internal/notify): channels the user set up in Settings,
-- events waiting for a channel's daily digest, and an outbox of messages
-- with retries, so a restart or a channel outage never loses or repeats one.

-- config holds the channel's address and secrets as JSON (a Discord webhook
-- URL, a Telegram bot token and chat ID, or a webhook URL and signing
-- secret); rules holds the events and filters (notify.Rules) as JSON.
CREATE TABLE notify_channels (
    id             INTEGER PRIMARY KEY,
    name           TEXT NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('discord', 'telegram', 'webhook')),
    config         TEXT NOT NULL,
    rules          TEXT NOT NULL,
    enabled        INTEGER NOT NULL DEFAULT 1,
    instant        INTEGER NOT NULL DEFAULT 1,
    digest_at      TEXT NOT NULL DEFAULT '', -- HH:MM in the user's time zone; '' for no digest
    digest_sent_at TEXT,                     -- the last digest slot handled
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
) STRICT;

-- Events (notify.Event as JSON) for a channel's next digest.
CREATE TABLE notify_pending (
    id         INTEGER PRIMARY KEY,
    channel_id INTEGER NOT NULL REFERENCES notify_channels (id) ON DELETE CASCADE,
    event      TEXT NOT NULL,
    created_at TEXT NOT NULL
) STRICT;
CREATE INDEX notify_pending_channel ON notify_pending (channel_id, id);

-- One message (a poll's events, a digest, a test) for a channel. Long ones
-- go out in parts; parts_sent lets a retry carry on where it stopped.
CREATE TABLE notify_outbox (
    id         INTEGER PRIMARY KEY,
    channel_id INTEGER NOT NULL REFERENCES notify_channels (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('poll', 'digest', 'test')),
    events     TEXT NOT NULL,  -- JSON array of notify.Event
    created_at TEXT NOT NULL,
    next_at    TEXT NOT NULL,  -- when to (re)try
    attempts   INTEGER NOT NULL DEFAULT 0,
    parts_sent INTEGER NOT NULL DEFAULT 0,
    sent_at    TEXT,
    failed_at  TEXT,           -- gave up after too many attempts
    last_error TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX notify_outbox_due ON notify_outbox (channel_id, id) WHERE sent_at IS NULL AND failed_at IS NULL;

-- Finished polls get notified about once; existing ones never are.
ALTER TABLE poll_runs ADD COLUMN notified_at TEXT;
UPDATE poll_runs SET notified_at = COALESCE(finished_at, started_at);
CREATE INDEX poll_runs_unnotified ON poll_runs (id) WHERE notified_at IS NULL;

CREATE INDEX changes_run ON changes (poll_run_id) WHERE poll_run_id IS NOT NULL;
