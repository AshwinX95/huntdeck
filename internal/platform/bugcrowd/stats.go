package bugcrowd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mdpsec/huntdeck/internal/platform"
)

// Program stats: each engagement's /statistics returns the numbers its
// brief shows (about 110 bytes, verified live 2026-10-26):
//
//	{"rewardedVulnerabilities":643,"averagePayout":"$569.04",
//	 "validSubmissionCount":null,"validationWithin":"6 days"}
//
// Bounty programs show rewarded reports and the average payout; VDPs show
// valid submissions instead.
//
// The brief also loads, and HuntDeck reads alongside (verified live
// 2026-10-28; none needs a login):
//
//	/engagements/{h}/recently_joined_users.json  {"users": [10 newest], "total": 544}
//	/engagements/{h}/hall_of_fames.json?page=1   {"presented_heroes": […],
//	                                              "pagination_meta": {"totalCount": 578}}
//	/engagements/{h}/announcements.json          {"announcements": [{"id", "title",
//	                                              "body" (HTML), "publishedAt"}]}
//	/engagements/{h}/scope_ranks                 {"scopeRank": 4}
//	/engagements/{h}/engagement_known_issues.json {"total": 12, "unique": 9}
//
// The scope rank is in the listing too (scopeRank), so it's only asked for
// when no listing ran in the last two days. The hunters joined and the hall
// of fame move slowly, so they're read about weekly (slowCountsAge) and the
// last values are reused in between; a restart reads them again. That is
// two requests per program on most days (four about weekly), so a daily
// refresh of the 520 public programs takes about 20 minutes at 1/s.
const statsBatch = 25

// slowCountsAge is how long a program's hunters joined and hall of fame
// counts are reused before they're read again.
const slowCountsAge = 7 * 24 * time.Hour

// slowCounts are a program's hunters joined and hackers thanked, and when
// they were read.
type slowCounts struct {
	hunters, thanked *int
	at               time.Time
}

type joinedJSON struct {
	Total *int `json:"total"`
}

type scopeRankJSON struct {
	ScopeRank *int `json:"scopeRank"`
}

type hallOfFameJSON struct {
	Meta *struct {
		TotalCount *int `json:"totalCount"`
	} `json:"pagination_meta"`
}

type announcementsJSON struct {
	Announcements []struct {
		ID          string    `json:"id"`
		Title       string    `json:"title"`
		Body        string    `json:"body"`
		PublishedAt time.Time `json:"publishedAt"`
	} `json:"announcements"`
}

type knownIssuesJSON struct {
	Total  *int `json:"total"`
	Unique *int `json:"unique"`
}

type statsJSON struct {
	Rewarded         *int    `json:"rewardedVulnerabilities"`
	AveragePayout    *string `json:"averagePayout"`
	ValidSubmissions *int    `json:"validSubmissionCount"`
	ValidationWithin *string `json:"validationWithin"`
}

// FetchProgramStats implements platform.StatsFetcher. Programs whose
// statistics can't be read (legacy briefs, a 404) are left out.
func (c *Client) FetchProgramStats(ctx context.Context, handles []string, save platform.StatsSaver) error {
	for start := 0; start < len(handles); start += statsBatch {
		batch := handles[start:min(start+statsBatch, len(handles))]
		out := make(map[string]platform.ProgramStats, len(batch))
		for _, h := range batch {
			if _, ok := handleFromBrief("/engagements/" + h); !ok {
				continue
			}
			var sj statsJSON
			err := c.getJSON(ctx, c.base.String()+"/engagements/"+h+"/statistics", &sj)
			switch {
			case err != nil && fatal(err):
				return fmt.Errorf("bugcrowd: program stats for %s: %w", h, err)
			case err != nil:
				continue
			}
			s, ok := sj.convert()
			if c.listedUnpriced(h) {
				// Listed as a bug bounty without rewards: it pays if it
				// publishes an average payout.
				pays := s.AvgBountyHigh != nil && *s.AvgBountyHigh > 0
				s.Facts, ok = &platform.Facts{OffersBounty: &pays}, true
			}
			extra, err := c.briefExtras(ctx, h, &s)
			if err != nil {
				return fmt.Errorf("bugcrowd: program stats for %s: %w", h, err)
			}
			if ok || extra {
				out[h] = s
			}
		}
		if err := save(batch, out); err != nil {
			return err
		}
	}
	return nil
}

// briefExtras adds what else the brief loads: hunters joined, hall of fame,
// the scope rank and the announcements. A part that can't be read is left
// out; ok reports whether any was read. Only fatal errors are returned.
func (c *Client) briefExtras(ctx context.Context, h string, s *platform.ProgramStats) (ok bool, err error) {
	base := c.base.String() + "/engagements/" + h
	counts, err := c.slowCounts(ctx, h)
	if err != nil {
		return false, err
	}
	s.Hunters, s.HackersThanked = counts.hunters, counts.thanked
	ok = s.Hunters != nil || s.HackersThanked != nil
	if r, listed := c.listedRank(h); listed {
		if r > 0 {
			s.ScopeRank, ok = &r, true
		}
	} else {
		var rank scopeRankJSON
		if err := c.getJSON(ctx, base+"/scope_ranks", &rank); err == nil && rank.ScopeRank != nil && *rank.ScopeRank > 0 {
			s.ScopeRank, ok = rank.ScopeRank, true
		} else if err != nil && fatal(err) {
			return false, err
		}
	}
	var known knownIssuesJSON
	if err := c.getJSON(ctx, base+"/engagement_known_issues.json", &known); err == nil && known.Total != nil {
		s.KnownIssues, s.KnownIssuesUnique, ok = known.Total, known.Unique, true
	} else if err != nil && fatal(err) {
		return false, err
	}
	var ann announcementsJSON
	if err := c.getJSON(ctx, base+"/announcements.json", &ann); err == nil {
		s.Announcements, ok = []platform.Announcement{}, true
		for _, a := range ann.Announcements {
			if a.ID == "" || a.PublishedAt.IsZero() {
				continue
			}
			s.Announcements = append(s.Announcements, platform.Announcement{ID: a.ID, Title: strings.TrimSpace(a.Title),
				Body: htmlToMarkdown(a.Body), PublishedAt: a.PublishedAt.UTC()})
		}
	} else if fatal(err) {
		return false, err
	}
	return ok, nil
}

// slowCounts returns a program's hunters joined and hall of fame counts:
// the last read if it's recent, else read now. Only fatal errors are
// returned; counts that couldn't be read are nil and asked for next time.
func (c *Client) slowCounts(ctx context.Context, h string) (slowCounts, error) {
	c.countsMu.Lock()
	sc, cached := c.counts[h]
	c.countsMu.Unlock()
	if cached && c.now().Sub(sc.at) < slowCountsAge {
		return sc, nil
	}
	base := c.base.String() + "/engagements/" + h
	sc = slowCounts{at: c.now()}
	var joined joinedJSON
	errJoined := c.getJSON(ctx, base+"/recently_joined_users.json", &joined)
	if errJoined != nil && fatal(errJoined) {
		return slowCounts{}, errJoined
	}
	var hof hallOfFameJSON
	errHOF := c.getJSON(ctx, base+"/hall_of_fames.json?page=1", &hof)
	if errHOF != nil && fatal(errHOF) {
		return slowCounts{}, errHOF
	}
	sc.hunters = joined.Total
	if hof.Meta != nil {
		sc.thanked = hof.Meta.TotalCount
	}
	// Both answered (a VDP may have no count): reuse them for a while.
	if errJoined == nil && errHOF == nil {
		c.countsMu.Lock()
		if c.counts == nil {
			c.counts = map[string]slowCounts{}
		}
		c.counts[h] = sc
		c.countsMu.Unlock()
	}
	return sc, nil
}

// convert maps the statistics; ok is false when they hold nothing.
func (sj statsJSON) convert() (platform.ProgramStats, bool) {
	s := platform.ProgramStats{RewardedReports: sj.Rewarded, ResolvedReports: sj.ValidSubmissions}
	if sj.AveragePayout != nil {
		if amount, cur, err := money(*sj.AveragePayout); err == nil {
			s.AvgBountyLow, s.AvgBountyHigh, s.Currency = &amount, &amount, cur
		}
	}
	if sj.ValidationWithin != nil {
		if h, err := hours(*sj.ValidationWithin); err == nil {
			s.ValidationHours = &h
		}
	}
	ok := s.RewardedReports != nil || s.ResolvedReports != nil || s.AvgBountyLow != nil || s.ValidationHours != nil
	return s, ok
}

// money reads "$569.04" or "€1,250".
func money(s string) (float64, string, error) {
	s = strings.TrimSpace(s)
	cur, _ := currency(s)
	if cur == "" {
		return 0, "", fmt.Errorf("unknown currency in %q", s)
	}
	_, size := utf8.DecodeRuneInString(s)
	v, err := strconv.ParseFloat(strings.ReplaceAll(s[size:], ",", ""), 64)
	if err != nil {
		return 0, "", err
	}
	return v, cur, nil
}

// hours reads durations like "6 days", "1 day" or "12 hours".
func hours(s string) (float64, error) {
	n, unit, ok := strings.Cut(strings.TrimSpace(s), " ")
	if !ok {
		return 0, fmt.Errorf("bad duration %q", s)
	}
	v, err := strconv.ParseFloat(n, 64)
	if err != nil {
		return 0, fmt.Errorf("bad duration %q", s)
	}
	switch strings.TrimSuffix(strings.ToLower(unit), "s") {
	case "hour":
		return v, nil
	case "day":
		return v * 24, nil
	case "week":
		return v * 24 * 7, nil
	case "month":
		return v * 24 * 30, nil
	}
	return 0, errors.New("unknown unit in " + strconv.Quote(s))
}
