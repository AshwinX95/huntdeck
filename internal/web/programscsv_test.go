package web

import (
	"encoding/csv"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// The Programs list as CSV: its filters, every matching program, and the
// columns asked for, money's currency once.
func TestProgramsCSV(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	read := func(path string) [][]string {
		t.Helper()
		res, body := e.do(c, http.MethodGet, path, nil, nil)
		if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/csv") ||
			!strings.Contains(res.Header.Get("Content-Disposition"), "huntdeck-programs.csv") {
			t.Fatalf("%s = %d %s", path, res.StatusCode, res.Header.Get("Content-Type"))
		}
		rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	rows := read("/programs.csv?cols=bounty,paid,avg,scope,bogus&sort=name")
	want := []string{"platform", "handle", "name", "visibility", "status", "bounty_min", "bounty_max", "bounty_currency",
		"paid_90d", "paid_90d_estimated", "stats_currency", "avg_bounty_low", "avg_bounty_high", "in_scope", "out_of_scope", "wildcards", "domains"}
	if !slices.Equal(rows[0], want) || len(rows) != 3 {
		t.Fatalf("header %q, %d rows", rows[0], len(rows))
	}
	i := slices.IndexFunc(rows, func(r []string) bool { return r[1] == "acme" })
	if i < 0 {
		t.Fatal("no acme row")
	}
	if acme := rows[i]; acme[1] != "acme" || acme[3] != "public" || acme[4] != "open" || acme[6] != "15000" || acme[8] != "41500" || len(acme) != len(want) {
		t.Errorf("acme = %q", acme)
	}
	if rows := read("/programs.csv?visibility=private"); len(rows) != 2 || rows[1][1] != "evil" || len(rows[0]) < 30 {
		t.Errorf("filtered, every column: %d rows, %d fields", len(rows), len(rows[0]))
	}
	if _, body := e.do(c, http.MethodGet, "/programs?visibility=private&page=1", nil, nil); !strings.Contains(body, `href="/programs.csv?visibility=private" data-csv-cols="program"`) {
		t.Error("the list doesn't link its CSV with its filters")
	}
	if got := csvText("=HYPERLINK(1)"); got != "'=HYPERLINK(1)" {
		t.Errorf("csvText = %q", got)
	}
}
