package db

// 资产级探针缓存：把 paths/js/methods 的确定性结果按资产复用，二次任务不必重复
// 枚举同一站点。payload 保留 srcprobe.Result 的 JSON，db 层不依赖该包。
//
// 缓存键含 target，资产 URL/端口变化时自动失效；expires_at 由写入方给 TTL。

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// ProbeCacheEntry is one cached probe result.
type ProbeCacheEntry struct {
	AssetID   int64           `json:"asset_id"`
	ProbeKey  string          `json:"probe_key"`
	Target    string          `json:"target"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
	ExpiresAt time.Time       `json:"expires_at"`
}

// GetProbeCache returns a non-expired cache entry, or nil when absent/expired.
func (d *DB) GetProbeCache(ctx context.Context, assetID int64, probeKey, target string) (*ProbeCacheEntry, error) {
	if d == nil || assetID <= 0 || probeKey == "" {
		return nil, nil
	}
	var e ProbeCacheEntry
	var payload []byte
	err := d.QueryRowContext(ctx, `
SELECT asset_id, probe_key, target, payload, created_at, expires_at
FROM asset_probe_cache
WHERE asset_id=$1 AND probe_key=$2 AND target=$3 AND expires_at > now()`,
		assetID, probeKey, target).Scan(&e.AssetID, &e.ProbeKey, &e.Target, &payload, &e.CreatedAt, &e.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.Payload = json.RawMessage(payload)
	return &e, nil
}

// PutProbeCache upserts a cache entry with the given TTL.
func (d *DB) PutProbeCache(ctx context.Context, assetID int64, probeKey, target string, payload []byte, ttl time.Duration) error {
	if d == nil || assetID <= 0 || probeKey == "" || len(payload) == 0 {
		return nil
	}
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	_, err := d.ExecContext(ctx, `
INSERT INTO asset_probe_cache(asset_id, probe_key, target, payload, created_at, expires_at)
VALUES ($1,$2,$3,$4::jsonb,now(),now()+$5::interval)
ON CONFLICT (asset_id, probe_key, target) DO UPDATE
SET payload=EXCLUDED.payload, created_at=now(), expires_at=EXCLUDED.expires_at`,
		assetID, probeKey, target, string(payload), ttl.String())
	return err
}

// PurgeExpiredProbeCache deletes expired rows (best-effort maintenance).
func (d *DB) PurgeExpiredProbeCache(ctx context.Context) (int64, error) {
	if d == nil {
		return 0, nil
	}
	res, err := d.ExecContext(ctx, `DELETE FROM asset_probe_cache WHERE expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
