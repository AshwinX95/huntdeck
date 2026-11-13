# Custom sources: platforms HuntDeck has no client for

HuntDeck reads HackerOne, Bugcrowd, Intigriti and YesWeHack itself. For any
other platform (Inspectiv, a company's own portal, a private platform you're
invited to), you can write a small script that reads the platform and saves
what it found as one JSON document. HuntDeck polls that document like a
platform:

- its programs and scope are stored;
- its changes (scope added, removed or edited, new and removed programs,
  bounties, status) show on Changes and notify like any platform's;
- its targets are on Targets, the scope check and the Burp/ZAP exports.

You choose how your script gets the data (an API, the website, an export).
HuntDeck never contacts the platform itself: it only reads the file or
address you configure.

There are two other ways to add programs:

- **Programs added by hand** (Programs › Add program) suit a few programs
  you keep up to date yourself.
- **A platform client in Go** suits a platform you'd like built in, with
  stats and invites; see [adding-a-platform.md](adding-a-platform.md).

## Setting one up

1. Have your script write the document (format below) somewhere HuntDeck can
   read it:
   - a file in the data directory, such as `/data/sources/inspectiv.json`
     (the container's `/data` is your `./data`);
   - or an address HuntDeck can reach, such as a small service on your
     private network.
2. Name the source and point HuntDeck at it, in `.env`:

   ```sh
   HUNTDECK_SOURCES=inspectiv=/data/sources/inspectiv.json
   # several, separated by spaces:
   # HUNTDECK_SOURCES="inspectiv=/data/sources/inspectiv.json acme=https://feeds.example.net/acme.json"
   # how often each is read in full (new programs every quarter of that):
   # HUNTDECK_SOURCES_INTERVAL=1h
   ```

   A name is 2 to 31 lowercase letters, digits and dashes. It's how the
   programs are stored and shown (`inspectiv` reads "Inspectiv", tagged
   "INS"), so don't rename it later: a new name is a new platform.
3. Restart HuntDeck. The source shows under Settings › Polling, where you
   can poll it now or change its intervals.
4. Run your script on a schedule of its own (cron, a systemd timer), as
   often as the platform is happy to be read.

Write the file atomically: write `inspectiv.json.tmp`, then rename it over
`inspectiv.json`. HuntDeck then never reads a half-written document.

## The document

```json
{
  "version": 1,
  "complete": true,
  "programs": [
    {
      "handle": "acme",
      "name": "Acme Corp",
      "url": "https://platform.example.com/programs/acme",
      "state": "open",
      "private": false,
      "offers_bounty": true,
      "currency": "USD",
      "bounty_min": 100,
      "bounty_max": 5000,
      "launched_at": "2026-01-15T00:00:00Z",
      "policy": "Markdown: the rules, what's out of bounds, how to report.",
      "scope": [
        {"id": "123", "identifier": "*.acme.example.com", "type": "wildcard", "max_severity": "critical"},
        {"identifier": "https://api.acme.example.com/v2", "instruction": "Send X-Test: your-handle."},
        {"identifier": "admin.acme.example.com", "in_scope": false}
      ]
    }
  ]
}
```

| Field | Required | What it is |
|---|---|---|
| `version` | yes | `1`. |
| `complete` | no, default `true` | `true` only if your script read every program the account can see. A program left out of a complete document is marked removed. Set it to `false` when a page failed, so nothing is removed by mistake. |
| `programs[].handle` | yes | Stable ID, as in the platform's URLs: letters, digits, `.`, `_`, `-`; up to 100. Unique in the document. Changing it makes a new program. |
| `programs[].name` | yes | As the platform shows it. |
| `programs[].url` | yes | The program's page, `http(s)://`. |
| `programs[].state` | no, default `open` | `open`, `paused` or `closed`. |
| `programs[].private` | no | `true` for invite-only programs. |
| `programs[].offers_bounty` | no | `true` if it pays bounties (else it's a VDP). |
| `programs[].currency` | no, default `USD` | Three capital letters. |
| `programs[].bounty_min`, `bounty_max` | no | The bounty range, in that currency. |
| `programs[].launched_at` | no | RFC 3339 time. |
| `programs[].policy` | no | Rules, in Markdown: shown on the Policy tab, and edits show as diffs. |
| `programs[].scope[].identifier` | yes | The asset as listed: `*.example.com`, `api.example.com`, a URL, an IP, a CIDR range, an app name… |
| `programs[].scope[].id` | no | The platform's own ID for the entry, if it has one: an edited identifier then stays one entry. Without it, entries are matched by what they say. |
| `programs[].scope[].type` | no | One of `wildcard`, `domain`, `url`, `api`, `ip`, `cidr`, `android`, `ios`, `windows`, `source_code`, `executable`, `hardware`, `smart_contract`, `ai_model`, `other`. Left out (or anything else), it's worked out from the identifier, and your value is kept as the platform's own type. |
| `programs[].scope[].in_scope` | no, default `true` | `false` for out-of-scope entries. |
| `programs[].scope[].bounty` | no | Bounty-eligible; default: in scope on a paying program. |
| `programs[].scope[].max_severity` | no | `none`, `low`, `medium`, `high` or `critical`. |
| `programs[].scope[].instruction` | no | Notes on the asset. |

Other fields are ignored, so you can keep your own alongside.

Limits:

- the whole document is at most 32 MB;
- at most 10,000 programs, and 5,000 scope entries per program.

A document with any mistake (bad JSON, a missing name, a handle listed
twice, …) is refused whole, and the poll fails with the reason in
Settings › Polling. Nothing stored changes until a good one arrives.

## A minimal script

The shape of one, in Python. How it reads the platform (`fetch_programs`)
is yours to write, within the platform's terms:

```python
import json, os

def fetch_programs():
    # Read the platform: its API, an export, your account's pages.
    # Return (programs, complete).
    ...

programs, complete = fetch_programs()
doc = {"version": 1, "complete": complete, "programs": programs}
tmp = "/srv/huntdeck/data/sources/inspectiv.json.tmp"
with open(tmp, "w") as f:
    json.dump(doc, f)
os.replace(tmp, tmp[:-4])  # atomic: HuntDeck never reads half a file
```

Keep credentials in the script's own configuration. HuntDeck's database
then holds only what the document says.

## What it doesn't do

- **No program stats.** Reports, payouts and response times aren't part of
  the format:
  - HuntScore scores these programs on fresh scope alone, with low
    confidence;
  - the Stats page shows their programs and scope, but no reports or
    payouts.
- **No invites or disclosed reports.**

A platform client in Go can do both ([adding-a-platform.md](adding-a-platform.md)).
