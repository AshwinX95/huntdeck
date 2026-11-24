# API and command line

The API page in the sidebar documents every endpoint and can send GETs for
you; `/api/v1/openapi.json` is the same as an OpenAPI 3.1 description.

There's no login and no token: scripts and curl work as they are against
your own HuntDeck.


Targets come back as plain text by default, streamed in full, so they pipe
straight into tools; everything else is JSON. Lists are paged: programs with
`limit` (default 100, max 1000) and `offset`, JSON targets with `limit`
(default 1000, max 5000) and `offset`, changes with `limit`; each returns `total`.
Plain-text targets take `limit` and `offset` too.


```sh
curl -s "http://127.0.0.1:8080/api/v1/targets?kind=wildcards&bounty=1"     # one per line
curl -s "http://127.0.0.1:8080/api/v1/targets?kind=domains&format=json&limit=1000&offset=0"
curl -s "http://127.0.0.1:8080/api/v1/programs?visibility=private"
curl -s "http://127.0.0.1:8080/api/v1/programs?sort=huntscore&limit=20"   # best to hunt, with hunt_score parts and reasons
curl -s "http://127.0.0.1:8080/api/v1/invites?invite=pending"          # invitations you haven't accepted yet
curl -s -X POST http://127.0.0.1:8080/api/v1/invites/intigriti/<handle>/accept  # accept one, then refresh that program
curl -s "http://127.0.0.1:8080/api/v1/programs/hackerone/security"     # everything its page shows
curl -s "http://127.0.0.1:8080/api/v1/board"     # the hunt board's summary
curl -s "http://127.0.0.1:8080/api/v1/changes?since=30d&program=hackerone/security"
curl -sO "http://127.0.0.1:8080/api/v1/programs/hackerone/security/scope.burp.json"   # or scope.zap.context, scope.caido.json
curl -s "http://127.0.0.1:8080/api/v1/stats?sort=perreport&bounty=1"     # program stats
curl -s "http://127.0.0.1:8080/api/v1/stats/platforms"     # the Stats page: every platform, with a year of months
curl -s "http://127.0.0.1:8080/api/v1/stats/platforms/hackerone?limit=20"     # a platform and its programs
curl -s "http://127.0.0.1:8080/api/v1/changes?since=24h"
curl -s "http://127.0.0.1:8080/api/v1/match?host=app.example.com"
curl -s "http://127.0.0.1:8080/api/v1/targets?kind=wildcards&sort=best"   # newest, most promising first
subfinder -d example.com -silent | curl -s --data-binary @- "http://127.0.0.1:8080/api/v1/match?format=txt"   # keep the in-scope hosts
curl -s "http://127.0.0.1:8080/api/v1/reports?status=you"     # your reports waiting on your reply
curl -s "http://127.0.0.1:8080/api/v1/reports/analytics?range=year"   # your numbers this year
curl -s "http://127.0.0.1:8080/api/v1/status"
curl -s -X POST "http://127.0.0.1:8080/api/v1/poll/hackerone?mode=quick"
```

**Your reports.** `/api/v1/reports` lists the reports on the Reports page,
latest activity first by default (`sort=submitted|activity|bounty|severity|program|state`),
with its filters (`status=open|you|stale|unpaid|valid|closed|paid`,
`platform`, `state`, `severity` and `year`, several comma-separated, `since`,
`q` to search titles and programs; every report unless `status` says
otherwise), paged with `limit` (default 100, max 1000) and `offset`, each with its program,
state (normalised, and the platform's own as `platform_state`), severities,
dates, who it waits on, its payments and earnings per currency.
`/api/v1/reports/analytics` is the Analytics page as numbers (`range`, or
`from` and `to` months). They hold titles and states only, never write-ups
or comments.

Target filters: `kind` (wildcards, domains, urls, ips, cidrs, `roots` for
root domains, `all`, or an asset type such as apps, android, ios or hardware), `scope=out`,
`platform`, `bounty=1`, `private=1`, `include_paused=1`, `tag`,
`include_ignored=1`. Programs take `tag` and `ignored=include|only`.

**Freshness.** `/api/v2/programs`, `/api/v2/invites` and
`/api/v2/programs/{platform}/{handle}` are v1's with each number that can be
estimated or that polls refresh (bounty range, scope counts, everything in
`numbers`) as `{value, estimated, source, updated_at}`: `source` is the
platform, `derived` (counted or worked out by HuntDeck) or `manual` (programs
added by hand), and `updated_at` moves only when the value does (null where
it can't be dated). v1 keeps the flat shape. Both give each program
`updated_at` (anything about it changed) and `last_synced_at` (last read from
its platform). The Methodology page says what's an estimate on each
platform. `since=<RFC 3339 time or 24h/7d>` on the program lists keeps
programs changed after it, on targets those added or edited after it.
Program lists and pages, invites and `GET /api/v1/match` send an `ETag` and `Last-Modified`:
send them back as `If-None-Match` / `If-Modified-Since` for a `304` while
nothing changed. Text exports (targets,
`match?format=txt`, the ZAP context) take `comment=1` for a first line
`# generated_at=…` (an XML comment in ZAP's), the rest unchanged; every
export, Burp's and Caido's JSON too, has the time in `X-HuntDeck-Generated-At`.

```sh
curl -s -D h.txt "http://127.0.0.1:8080/api/v2/programs?since=7d" -o programs.json
curl -s -o /dev/null -w '%{http_code}\n' -H "If-None-Match: $(sed -n 's/^[Ee][Tt]ag: //p' h.txt | tr -d '\r')" \
  "http://127.0.0.1:8080/api/v2/programs?since=7d"   # 304 until something changes
curl -s "http://127.0.0.1:8080/api/v1/targets?kind=wildcards&since=24h&comment=1"
```

## Command line

The same data from inside the container (`docker compose exec huntdeck /huntdeck …`):

```sh
huntdeck status                       # what's stored, recent polls
huntdeck export wildcards             # *.example.com, one per line
huntdeck export domains --bounty      # only bounty-eligible scope on paying programs
huntdeck export domains --private     # only your private programs
huntdeck export domains --oos         # out-of-scope list, for filtering
huntdeck export wildcards --tag focus # only programs you tagged "focus"
huntdeck match https://app.example.com/login   # which programs cover this, in or out?
subfinder -d example.com -silent | huntdeck match -       # print only the in-scope hosts
huntdeck match -all - < hosts.txt     # every host: in, out or none, with its programs
huntdeck changes --since 72h          # new programs, invites, scope added/removed
huntdeck score -n 25                  # rescore now and list the top HuntScores
huntdeck poll --limit 20              # poll now (only the first 20 programs)
huntdeck poll --platform bc --refetch-scope   # download every scope now, even unchanged ones
huntdeck stats security               # fetch a HackerOne program's stats now (--platform for others)
huntdeck reports --platform h1        # read your own reports now (prints counts, never titles)
huntdeck seed export /data/seed-out.db.gz   # public programs' data, for a new install to start from
huntdeck seed import /data/seed.db.gz       # load one into a new, empty install
```

```sh
docker compose exec -T huntdeck /huntdeck export wildcards | sed 's/^\*\.//' | subfinder -silent
```

Exports skip paused and closed programs unless you pass `--include-paused`.
A wildcard `*.example.com` covers subdomains, not `example.com` itself. Always
check the program's rules before testing.
