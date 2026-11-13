-- Invitations to private programs not yet accepted, from platforms that
-- list them (platform.InviteLister: Intigriti's web app). invite_pending is
-- 1 while the invitation waits; invited_at is when it was sent. When it
-- leaves the list, joined_at is set to when HuntDeck saw that, unless the
-- platform says when.
ALTER TABLE programs ADD COLUMN invited_at TEXT;
ALTER TABLE programs ADD COLUMN invite_pending INTEGER NOT NULL DEFAULT 0;
