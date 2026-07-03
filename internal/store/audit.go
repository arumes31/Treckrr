package store

import (
	"context"
	"database/sql"

	"treckrr/internal/models"
)

// AddAudit records one action in the audit trail.
func (s *Store) AddAudit(ctx context.Context, userID *int64, username, action, entity, entityID, detail, ip string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_log (user_id, username, action, entity, entity_id, detail, ip)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		nullInt(userID), username, action, entity, entityID, detail, ip)
	return err
}

// ListAudit returns the most recent audit entries, newest first.
func (s *Store) ListAudit(ctx context.Context, limit int) ([]models.AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, username, action, entity, entity_id, detail, ip, created_at
		  FROM audit_log ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.AuditEntry
	for rows.Next() {
		var (
			e   models.AuditEntry
			uid sql.NullInt64
		)
		if err := rows.Scan(&e.ID, &uid, &e.Username, &e.Action, &e.Entity,
			&e.EntityID, &e.Detail, &e.IP, &e.Created); err != nil {
			return nil, err
		}
		if uid.Valid {
			id := uid.Int64
			e.UserID = &id
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
