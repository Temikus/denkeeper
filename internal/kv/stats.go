package kv

import (
	"context"
	"errors"
	"fmt"
)

// NamespaceStat counts one agent's live keys in one namespace: the text
// before the first ':', or "" for a key without one.
type NamespaceStat struct {
	Namespace string `db:"namespace"`
	Keys      int    `db:"keys"`
	NoExpiry  int    `db:"no_expiry"`
}

// NamespaceStats counts an agent's live keys per namespace, most keys first,
// keys without a namespace last among equals. An empty agent is an error:
// there is no all-agents form.
func (s *SQLiteStore) NamespaceStats(ctx context.Context, agent string) ([]NamespaceStat, error) {
	if agent == "" {
		return nil, errors.New("kv namespace stats: agent is required")
	}
	var out []NamespaceStat
	err := s.db.SelectContext(ctx, &out, `
		SELECT CASE WHEN instr(key, ':') > 0 THEN substr(key, 1, instr(key, ':') - 1) ELSE '' END AS namespace,
		       COUNT(*) AS keys,
		       SUM(CASE WHEN expires_at IS NULL THEN 1 ELSE 0 END) AS no_expiry
		FROM kv
		WHERE agent_name = ? AND (expires_at IS NULL OR expires_at > datetime('now'))
		GROUP BY namespace
		ORDER BY keys DESC, namespace = '' ASC, namespace ASC`, agent)
	if err != nil {
		return nil, fmt.Errorf("kv namespace stats: %w", err)
	}
	if out == nil {
		out = []NamespaceStat{}
	}
	return out, nil
}
