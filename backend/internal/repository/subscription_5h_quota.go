package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *dynamic5hPolicyRepository) ReadSubscription5hQuota(ctx context.Context, userID int64, now time.Time, multiplier float64) (service.Subscription5hQuota, error) {
	var result service.Subscription5hQuota
	var recoverAt, lastUsedAt sql.NullTime
	err := r.db.QueryRowContext(ctx, `WITH weekly AS (
  SELECT COALESCE(SUM(g.weekly_limit_usd), 0) AS amount
  FROM user_subscriptions s JOIN groups g ON g.id=s.group_id
  WHERE s.user_id=$1 AND s.status='active' AND s.starts_at<=$2 AND s.expires_at>$2
    AND s.deleted_at IS NULL AND g.deleted_at IS NULL AND g.status='active'
    AND g.subscription_type='subscription' AND g.weekly_limit_usd>0
), spending AS (
  SELECT created_at, SUM(GREATEST(actual_cost, 0)) AS amount
  FROM usage_logs
  WHERE user_id=$1 AND created_at>$2-INTERVAL '5 hours' AND created_at<=$2
    AND created_at>COALESCE((SELECT MAX(created_at) FROM dynamic_5h_audit_logs
      WHERE user_id=$1 AND action='usage_reset'), '-infinity'::timestamptz)
    AND actual_cost>0
  GROUP BY created_at
), rolling AS (
  SELECT created_at, amount, SUM(amount) OVER (ORDER BY created_at) AS expired,
    SUM(amount) OVER () AS total
  FROM spending
)
SELECT (SELECT amount FROM weekly), COALESCE(MAX(total), 0),
  MIN(created_at + INTERVAL '5 hours') FILTER (
    WHERE total < (SELECT amount FROM weekly)*0.15*$3
       OR total-expired < (SELECT amount FROM weekly)*0.15*$3),
  MAX(created_at)
FROM rolling`, userID, now, multiplier).Scan(&result.WeeklyLimitUSD, &result.UsageUSD, &recoverAt, &lastUsedAt)
	if recoverAt.Valid {
		result.RecoverAt = &recoverAt.Time
	}
	if lastUsedAt.Valid {
		result.LastUsedAt = &lastUsedAt.Time
	}
	return result, err
}

func (r *dynamic5hPolicyRepository) ListSubscription5hUserIDs(ctx context.Context, now time.Time) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT s.user_id FROM user_subscriptions s
JOIN groups g ON g.id=s.group_id JOIN users u ON u.id=s.user_id
WHERE s.status='active' AND s.starts_at<=$1 AND s.expires_at>$1 AND s.deleted_at IS NULL
AND u.deleted_at IS NULL AND g.deleted_at IS NULL AND g.status='active'
AND g.subscription_type='subscription' AND g.weekly_limit_usd>0 ORDER BY s.user_id`, now)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		result = append(result, userID)
	}
	return result, rows.Err()
}
