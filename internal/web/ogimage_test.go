package web

import (
	"bytes"
	"image/png"
	"net/http"
	"strings"
	"testing"

	"github.com/mdpsec/huntdeck/internal/store"
)

func TestOGCard(t *testing.T) {
	top, paid := 5000.0, 12345.0
	for _, tt := range []struct {
		name string
		p    store.ProgramListItem
		want [4]string // the tiles' values
	}{
		{"paying", store.ProgramListItem{ProgramRef: store.ProgramRef{Name: "Acme", Platform: "hackerone", State: "open"}, OffersBounty: true,
			BountyMax: &top, Currency: "USD", PaidAll: &paid, PaidAllEstimated: true, StatsCurrency: "USD", InScope: 3, Wildcards: 1,
			Hunt: &store.HuntDetail{Score: 71.6, Confidence: "high"}},
			[4]string{"72", "$5k", "≈$12k", "3 assets"}},
		{"vdp, long name", store.ProgramListItem{ProgramRef: store.ProgramRef{Name: strings.Repeat("Very Long Program Name ", 12), Platform: "acme-portal", State: "paused"}, InScope: 1},
			[4]string{"—", "VDP", "—", "1 asset"}},
	} {
		c := newOGCard(tt.p, "demo.example.com")
		for i, w := range tt.want {
			if c.Tiles[i].Value != w {
				t.Errorf("%s: tile %d = %q, want %q", tt.name, i, c.Tiles[i].Value, w)
			}
		}
		b, err := c.draw()
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil || img.Bounds().Dx() != ogWidth || img.Bounds().Dy() != ogHeight {
			t.Errorf("%s: %v, %v", tt.name, err, img.Bounds())
		}
	}
}

// Public programs have a share image; private ones and unknown ones none.
func TestProgramOGImage(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	for path, want := range map[string]int{
		"/programs/fake/acme/og.png": http.StatusOK,
		"/programs/fake/evil/og.png": http.StatusNotFound, // private
		"/programs/fake/nope/og.png": http.StatusNotFound,
	} {
		res, _ := e.do(c, http.MethodGet, path, nil, nil)
		if res.StatusCode != want || (want == http.StatusOK && res.Header.Get("Content-Type") != "image/png") {
			t.Errorf("%s = %d %s, want %d", path, res.StatusCode, res.Header.Get("Content-Type"), want)
		}
	}
	if _, body := e.do(c, http.MethodGet, "/programs/fake/evil", nil, nil); strings.Contains(body, "/og.png") {
		t.Error("private program names a share image")
	}
}
