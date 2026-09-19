package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type dynamic5hPolicyRepository struct { db *sql.DB }

func NewDynamic5hPolicyRepository(db *sql.DB) service.Dynamic5hPolicyRepository {
	return &dynamic5hPolicyRepository{db: db}
}

func (r *dynamic5hPolicyRepository) GetUserPolicy(ctx context.Context, userID int64) (service.Dynamic5hUserPolicy, bool, error) {
	var policy service.Dynamic5hUserPolicy
	err := r.db.QueryRowContext(ctx, `SELECT exempt, multiplier FROM dynamic_5h_user_policies WHERE user_id = $1`, userID).Scan(&policy.Exempt, &policy.Multiplier)
	if err == sql.ErrNoRows { return service.Dynamic5hUserPolicy{Multiplier: 1}, false, nil }
	return policy, err == nil, err
}

func (r *dynamic5hPolicyRepository) UpsertUserPolicy(ctx context.Context, userID int64, policy service.Dynamic5hUserPolicy, actorUserID int64, reason string) error {
	tx, err := r.db.BeginTx(ctx, nil); if err != nil { return err }
	defer func() { _ = tx.Rollback() }()
	old, oldFound, err := dynamic5hPolicyTx(ctx, tx, userID); if err != nil { return err }
	var actor any
	if actorUserID > 0 { actor = actorUserID }
	if _, err = tx.ExecContext(ctx, `INSERT INTO dynamic_5h_user_policies (user_id, multiplier, exempt, updated_by, updated_at)
VALUES ($1,$2,$3,$4,NOW()) ON CONFLICT (user_id) DO UPDATE SET multiplier=EXCLUDED.multiplier, exempt=EXCLUDED.exempt, updated_by=EXCLUDED.updated_by, updated_at=NOW()`, userID, policy.Multiplier, policy.Exempt, actor); err != nil { return err }
	newJSON, _ := json.Marshal(policy)
	var oldJSON any
	if oldFound { encoded, _ := json.Marshal(old); oldJSON = string(encoded) }
	if _, err = tx.ExecContext(ctx, `INSERT INTO dynamic_5h_audit_logs (user_id, actor_user_id, action, reason, old_policy, new_policy) VALUES ($1,$2,'policy_update',$3,$4,$5)`, userID, actor, reason, oldJSON, string(newJSON)); err != nil { return err }
	return tx.Commit()
}

func dynamic5hPolicyTx(ctx context.Context, tx *sql.Tx, userID int64) (service.Dynamic5hUserPolicy, bool, error) {
	var p service.Dynamic5hUserPolicy
	err := tx.QueryRowContext(ctx, `SELECT exempt, multiplier FROM dynamic_5h_user_policies WHERE user_id=$1 FOR UPDATE`, userID).Scan(&p.Exempt, &p.Multiplier)
	if err == sql.ErrNoRows { return service.Dynamic5hUserPolicy{Multiplier: 1}, false, nil }
	return p, err == nil, err
}

func (r *dynamic5hPolicyRepository) RecordUsageReset(ctx context.Context, userID, actorUserID int64, reason string, before, after float64) error {
	var actor any
	if actorUserID > 0 { actor = actorUserID }
	_, err := r.db.ExecContext(ctx, `INSERT INTO dynamic_5h_audit_logs (user_id, actor_user_id, action, reason, before_usage, after_usage) VALUES ($1,$2,'usage_reset',$3,$4,$5)`, userID, actor, reason, before, after)
	return err
}

func (r *dynamic5hPolicyRepository) ListAuditLogs(ctx context.Context, limit int) ([]service.Dynamic5hAuditLog, error) {
	if limit <= 0 || limit > 200 { limit = 50 }
	rows, err := r.db.QueryContext(ctx, `SELECT a.id,a.user_id,a.actor_user_id,COALESCE(u.email,''),a.action,a.reason,a.before_usage,a.after_usage,a.old_policy,a.new_policy,a.created_at
FROM dynamic_5h_audit_logs a LEFT JOIN users u ON u.id=a.actor_user_id ORDER BY a.created_at DESC LIMIT $1`, limit)
	if err != nil { return nil, err }
	defer rows.Close()
	result := make([]service.Dynamic5hAuditLog, 0, limit)
	for rows.Next() {
		var item service.Dynamic5hAuditLog
		var oldJSON, newJSON []byte
		if err := rows.Scan(&item.ID,&item.UserID,&item.ActorUserID,&item.ActorEmail,&item.Action,&item.Reason,&item.BeforeUsage,&item.AfterUsage,&oldJSON,&newJSON,&item.CreatedAt); err != nil { return nil, err }
		if len(oldJSON) > 0 { var p service.Dynamic5hUserPolicy; if json.Unmarshal(oldJSON,&p)==nil { item.OldPolicy=&p } }
		if len(newJSON) > 0 { var p service.Dynamic5hUserPolicy; if json.Unmarshal(newJSON,&p)==nil { item.NewPolicy=&p } }
		result = append(result,item)
	}
	if err := rows.Err(); err != nil { return nil, fmt.Errorf("list dynamic 5h audit logs: %w", err) }
	return result,nil
}
