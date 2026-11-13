package store

import (
	"context"
	"encoding/json"
	"time"
)

// WebSyncSettings are a platform's browser sync settings (see
// platform.WebSyncer).
type WebSyncSettings struct {
	Enabled  bool          `json:"enabled"`
	Interval time.Duration `json:"interval"` // between runs
}

// DefaultWebSync is used until settings are saved: on, once a day.
var DefaultWebSync = WebSyncSettings{Enabled: true, Interval: 24 * time.Hour}

const webSyncPrefix = "websync."

// WebSync returns a platform's browser sync settings, or the default (with
// saved false) if none were saved or they can't be read.
func (s *Store) WebSync(ctx context.Context, platformName string) (ws WebSyncSettings, saved bool, err error) {
	m, err := s.Settings(ctx, webSyncPrefix+platformName)
	if err != nil {
		return DefaultWebSync, false, err
	}
	raw, ok := m[webSyncPrefix+platformName]
	if !ok || json.Unmarshal([]byte(raw), &ws) != nil || ws.Interval <= 0 {
		return DefaultWebSync, false, nil
	}
	return ws, true, nil
}

// SaveWebSync stores a platform's browser sync settings.
func (s *Store) SaveWebSync(ctx context.Context, platformName string, ws WebSyncSettings) error {
	b, err := json.Marshal(ws)
	if err != nil {
		return err
	}
	return s.Write(ctx, func(tx *Tx) error { return tx.PutSetting(ctx, webSyncPrefix+platformName, string(b)) })
}

// ResetWebSync drops a platform's saved browser sync settings.
func (s *Store) ResetWebSync(ctx context.Context, platformName string) error {
	return s.Write(ctx, func(tx *Tx) error { return tx.DeleteSetting(ctx, webSyncPrefix+platformName) })
}
