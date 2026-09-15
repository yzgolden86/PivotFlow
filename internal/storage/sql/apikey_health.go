package sql

import (
	"context"
	"log"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// recordAPIKeyHealth runs after log commit, so an observation failure must never
// cause the caller to retry already-persisted logs. Aggregate before touching DB.
func (s *SQLStore) recordAPIKeyHealth(ctx context.Context, entries []*model.LogEntry) {
	latest := make(map[int64]map[string]model.APIKeyHealth)
	for _, entry := range entries {
		health, ok := model.ObserveAPIKeyHealth(entry)
		if !ok {
			continue
		}
		if latest[entry.ChannelID] == nil {
			latest[entry.ChannelID] = make(map[string]model.APIKeyHealth)
		}
		if health.CheckedAt >= latest[entry.ChannelID][entry.APIKeyUsed].CheckedAt {
			latest[entry.ChannelID][entry.APIKeyUsed] = health
		}
	}
	for channelID, byValue := range latest {
		keys, err := s.GetAPIKeys(ctx, channelID)
		if err != nil {
			log.Printf("[WARN] read API key health targets (channel=%d): %v", channelID, err)
			continue
		}
		for _, key := range keys {
			health, ok := byValue[key.APIKey]
			if !ok || health.CheckedAt < key.Health.CheckedAt {
				continue
			}
			// Indices shift after deletion. Match the actual credential then its
			// persistent ID; removed/replaced credentials cannot taint another key.
			_, err := s.ExecContext(ctx, `UPDATE api_keys SET health_status = ?, health_reason = ?,
				health_status_code = ?, health_checked_at = ? WHERE id = ? AND health_checked_at <= ?`,
				health.Status, health.Reason, health.StatusCode, health.CheckedAt, key.ID, health.CheckedAt)
			if err != nil {
				log.Printf("[WARN] persist API key health (channel=%d, key_id=%d): %v", channelID, key.ID, err)
			}
		}
	}
}
