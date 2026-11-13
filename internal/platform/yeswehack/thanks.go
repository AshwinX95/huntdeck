package yeswehack

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// A program's Thanks list (the "Thanks" tab of its page) names each hunter
// with how many of their reports it accepted (verified live 2026-11-05):
//
//	GET /programs/{slug}/thanks?page=N
//	→ {"pagination": {"page": 1, "nb_pages": 21, "results_per_page": 12, "nb_results": 251},
//	   "items": [{"username": "…", "slug": "…", "accepted_reports": 2, …}]}
//
// Pages hold 12 hunters whatever is asked for. Programs that hide the list
// answer 404 (7 of 53 public ones in 2026-11). Its order isn't documented;
// HuntDeck takes it to be the hunters whose count last changed first, so
// a daily read stops at the first page it already knows (the syncer
// re-reads everything weekly in case it isn't). Counted from every page,
// GovTech's VDP had 564 valid reports out of 1,092 received.

type thanksJSON struct {
	Pagination struct {
		Pages   int `json:"nb_pages"`
		Results int `json:"nb_results"`
	} `json:"pagination"`
	Items []struct {
		Slug     string `json:"slug"`
		Accepted int    `json:"accepted_reports"`
	} `json:"items"`
}

// thanksMaxPages bounds one list: Dojo, the practice program, had 173.
const thanksMaxPages = 400

// FetchThanks implements platform.ThanksFetcher.
func (c *Client) FetchThanks(ctx context.Context, handle string, known map[string]int) (platform.Thanks, error) {
	t := platform.Thanks{Hunters: map[string]int{}}
	for page := 1; page <= thanksMaxPages; page++ {
		var tj thanksJSON
		u := fmt.Sprintf("%s/programs/%s/thanks?page=%d", c.base.String(), url.PathEscape(handle), page)
		err := c.getJSON(ctx, u, &tj)
		switch {
		case page == 1 && errors.Is(err, errNotFound):
			return platform.Thanks{}, nil
		case err != nil:
			return platform.Thanks{}, fmt.Errorf("yeswehack: thanks of %s page %d: %w", handle, page, err)
		}
		t.Shown, t.Listed = true, tj.Pagination.Results
		same := known != nil
		for _, it := range tj.Items {
			if it.Slug == "" {
				continue
			}
			t.Hunters[it.Slug] = it.Accepted
			if n, ok := known[it.Slug]; !ok || n != it.Accepted {
				same = false
			}
		}
		if page >= tj.Pagination.Pages || len(tj.Items) == 0 {
			t.Complete = true
			return t, nil
		}
		if same {
			return t, nil
		}
	}
	return t, nil
}
