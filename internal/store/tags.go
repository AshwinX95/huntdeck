package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// Tag limits: tags are short labels, never free text.
const (
	MaxTags      = 20
	MaxTagLength = 32
)

// NormalizeTags reads tags as typed (comma or space separated), lowercased,
// deduplicated and sorted. A tag is letters, digits, '-', '_' and '.'.
func NormalizeTags(s string) ([]string, error) {
	var out []string
	for _, t := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		if len(t) > MaxTagLength {
			return nil, fmt.Errorf("tag %q is longer than %d characters", t, MaxTagLength)
		}
		if strings.ContainsFunc(t, func(r rune) bool { return !tagRune(r) }) {
			return nil, fmt.Errorf("tag %q: use letters, digits, '-', '_' or '.'", t)
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	if len(out) > MaxTags {
		return nil, fmt.Errorf("at most %d tags", MaxTags)
	}
	slices.Sort(out)
	return out, nil
}

func tagRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
}

// SetIgnored marks a program ignored or not.
func (s *Store) SetIgnored(ctx context.Context, programID int64, ignored bool) error {
	return s.Write(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx, `UPDATE programs SET ignored = ? WHERE id = ?`, b2i(ignored), programID)
		return err
	})
}

// SetTags replaces a program's tags (already normalised).
func (s *Store) SetTags(ctx context.Context, programID int64, tags []string) error {
	return s.Write(ctx, func(tx *Tx) error {
		if _, err := tx.tx.ExecContext(ctx, `DELETE FROM program_tags WHERE program_id = ?`, programID); err != nil {
			return err
		}
		for _, t := range tags {
			if _, err := tx.tx.ExecContext(ctx, `INSERT INTO program_tags (program_id, tag) VALUES (?, ?)`, programID, t); err != nil {
				return err
			}
		}
		return nil
	})
}

// TagCount is a tag and how many live programs have it.
type TagCount struct {
	Tag      string
	Programs int
}

// Tags returns every tag on a live program, by name.
func (s *Store) Tags(ctx context.Context) ([]TagCount, error) {
	rows, err := s.db.R.QueryContext(ctx, `
		SELECT t.tag, COUNT(*) FROM program_tags t JOIN programs p ON p.id = t.program_id
		WHERE p.removed_at IS NULL GROUP BY t.tag ORDER BY t.tag`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TagCount
	for rows.Next() {
		var t TagCount
		if err := rows.Scan(&t.Tag, &t.Programs); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// tagsColumn reads program p's tags as "a,b,c" (tags never hold commas).
const tagsColumn = `COALESCE((SELECT GROUP_CONCAT(tag, ',') FROM (SELECT tag FROM program_tags WHERE program_id = p.id ORDER BY tag)), '')`

// tagCond keeps programs (alias p) that have the tag.
const tagCond = `EXISTS (SELECT 1 FROM program_tags pt WHERE pt.program_id = p.id AND pt.tag = ?)`

func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
