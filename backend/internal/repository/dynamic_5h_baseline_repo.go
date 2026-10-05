package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *dynamic5hPolicyRepository) ObservePlusBaseline(ctx context.Context, observations []service.Dynamic5hPlusObservation, now time.Time) (float64, int, error) {
	var baseline float64
	var samples int
	err := r.db.QueryRowContext(ctx, `SELECT capacity, sample_count FROM dynamic_5h_plus_baseline WHERE id=1`).Scan(&baseline, &samples)
	if err != sql.ErrNoRows {
		return baseline, samples, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, observation := range observations {
		if observation.UsedPercent <= 5 {
			_, err = tx.ExecContext(ctx, `INSERT INTO dynamic_5h_plus_samples (account_id,reset_at,started_at,start_percent)
VALUES ($1,$2,$3,$4) ON CONFLICT (account_id,reset_at) DO NOTHING`, observation.AccountID, observation.ResetAt, observation.ObservedAt, observation.UsedPercent)
		} else if observation.UsedPercent >= 95 {
			_, err = tx.ExecContext(ctx, `UPDATE dynamic_5h_plus_samples SET ended_at=$3,end_percent=$4
WHERE account_id=$1 AND reset_at=$2 AND ended_at IS NULL AND started_at<$3`, observation.AccountID, observation.ResetAt, observation.ObservedAt, observation.UsedPercent)
		}
		if err != nil {
			return 0, 0, err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE dynamic_5h_plus_samples s SET capacity=cost.amount/((s.end_percent-s.start_percent)/100.0)
FROM (
 SELECT s2.account_id,s2.reset_at,SUM(COALESCE(u.account_stats_cost,u.total_cost)) AS amount
 FROM dynamic_5h_plus_samples s2 JOIN usage_logs u ON u.account_id=s2.account_id AND u.created_at>s2.started_at AND u.created_at<=s2.ended_at
 WHERE s2.capacity IS NULL AND s2.ended_at<=$1 AND s2.end_percent-s2.start_percent>=90
 GROUP BY s2.account_id,s2.reset_at
 HAVING SUM(COALESCE(u.account_stats_cost,u.total_cost))>0
 AND COUNT(*) FILTER (WHERE COALESCE(u.account_stats_cost,u.total_cost)<=0 OR COALESCE(u.account_stats_cost,u.total_cost) IS NULL)=0
) cost WHERE s.account_id=cost.account_id AND s.reset_at=cost.reset_at`, now.Add(-2*time.Minute))
	if err != nil {
		return 0, 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO dynamic_5h_plus_baseline (id,capacity,sample_count)
SELECT 1,percentile_cont(0.5) WITHIN GROUP (ORDER BY capacity),COUNT(*) FROM
(SELECT capacity FROM dynamic_5h_plus_samples WHERE capacity>0 ORDER BY ended_at,account_id LIMIT 3) samples
HAVING COUNT(*)=3 ON CONFLICT (id) DO NOTHING`)
	if err != nil {
		return 0, 0, err
	}
	err = tx.QueryRowContext(ctx, `SELECT capacity,sample_count FROM dynamic_5h_plus_baseline WHERE id=1`).Scan(&baseline, &samples)
	if err == sql.ErrNoRows {
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM dynamic_5h_plus_samples WHERE capacity>0`).Scan(&samples)
	}
	if err != nil {
		return 0, 0, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM dynamic_5h_plus_samples WHERE capacity IS NULL AND reset_at<$1`, now.Add(-30*24*time.Hour)); err != nil {
		return 0, 0, err
	}
	return baseline, samples, tx.Commit()
}
