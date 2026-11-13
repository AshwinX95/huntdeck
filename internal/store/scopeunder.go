package store

import (
	"context"
	"strings"

	"github.com/mdpsec/huntdeck/internal/scope"
)

// Scope the scope check shows beside its verdict: what's listed under a
// host, or, for text that isn't one, what mentions it.

// ScopeUnder returns live scope at or under input's host that doesn't
// decide it: wildcards on the host itself ("*.example.com" covers
// subdomains, not example.com) and anything on its subdomains. Nothing
// for an IP. See relatedScope for the order and limit.
func (s *Store) ScopeUnder(ctx context.Context, input string, limit int) ([]scope.Rule, map[int64]ProgramRef, int, error) {
	hosts, isIP, ok := scope.Candidates(input)
	if !ok || isIP {
		return nil, map[int64]ProgramRef{}, 0, nil
	}
	host := hosts[0]
	like := "%." + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(host)
	return s.relatedScope(ctx, `t.host <> '' AND ((t.host = ? AND t.kind = 'wildcard') OR t.host LIKE ? ESCAPE '\')`,
		[]any{host, like}, host, limit)
}

// MinMention is the shortest text ScopeMentioning looks for.
const MinMention = 3

// ScopeMentioning returns live scope whose target contains text, as the
// Targets list's search finds it ("canva-apps" finds *.canva-apps.cn and
// *.canva-apps.com): for a scope check of something that isn't a listed
// host or under one. Nothing for text shorter than MinMention. See
// relatedScope for the order and limit.
func (s *Store) ScopeMentioning(ctx context.Context, text string, limit int) ([]scope.Rule, map[int64]ProgramRef, int, error) {
	text = strings.ToLower(strings.TrimSpace(text))
	if len(text) < MinMention {
		return nil, map[int64]ProgramRef{}, 0, nil
	}
	return s.relatedScope(ctx, `lower(t.value) LIKE ? ESCAPE '\'`, []any{likeEscape(text)}, text, limit)
}

// relatedScope returns live scope matching cond, each target once per
// program and verdict (entries often share one: several URLs on a host):
// those whose host is first first (wildcards before the rest), then by
// host, shortest first; at most limit, with how many there are in all.
func (s *Store) relatedScope(ctx context.Context, cond string, args []any, first string, limit int) ([]scope.Rule, map[int64]ProgramRef, int, error) {
	progs := map[int64]ProgramRef{}
	var total int
	//nolint:gosec // constant fragments; values are bound parameters
	if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT DISTINCT p.id, e.in_scope, t.kind, t.value FROM scope_targets t
		JOIN scope_entries e ON e.id = t.scope_entry_id JOIN programs p ON p.id = e.program_id
		WHERE e.removed_at IS NULL AND p.removed_at IS NULL AND `+cond+`)`, args...).Scan(&total); err != nil {
		return nil, progs, 0, err
	}
	rules, err := s.queryRules(ctx, cond+` GROUP BY p.id, e.in_scope, t.kind, t.value
		ORDER BY t.host <> ?, t.kind <> 'wildcard', length(t.host), t.host, lower(p.name), t.value LIMIT ?`,
		append(args, first, limit), progs)
	return rules, progs, total, err
}
