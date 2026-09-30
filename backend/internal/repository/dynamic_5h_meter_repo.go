package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *dynamic5hPolicyRepository) EnqueueMeterEvent(ctx context.Context, event service.Dynamic5hMeterEvent) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO dynamic_5h_meter_events (source_key,user_id,request_id,amount,occurred_at)
VALUES ($1,$2,$3,$4,$5) ON CONFLICT (source_key) DO NOTHING`, event.SourceKey, event.UserID, event.RequestID, event.Amount, event.OccurredAt)
	return err
}

func (r *dynamic5hPolicyRepository) ListMeterEvents(ctx context.Context, afterID int64, pendingOnly bool, cutoff time.Time, limit int) ([]service.Dynamic5hMeterEvent, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT e.id,e.user_id,e.request_id,e.amount,e.occurred_at,
CASE WHEN e.occurred_at <= COALESCE((SELECT MAX(a.created_at) FROM dynamic_5h_audit_logs a WHERE a.user_id=e.user_id AND a.action='usage_reset'), '-infinity'::timestamptz) THEN 0 ELSE e.amount END
FROM dynamic_5h_meter_events e WHERE e.id>$1 AND (NOT $2 OR e.delivered_at IS NULL) AND e.occurred_at >= $3
ORDER BY e.id LIMIT $4`, afterID, pendingOnly, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	events := make([]service.Dynamic5hMeterEvent, 0, limit)
	for rows.Next() {
		var event service.Dynamic5hMeterEvent
		if err := rows.Scan(&event.ID, &event.UserID, &event.RequestID, &event.Amount, &event.OccurredAt, &event.UserAmount); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (r *dynamic5hPolicyRepository) AcknowledgeMeterEvent(ctx context.Context, eventID int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE dynamic_5h_meter_events SET delivered_at=COALESCE(delivered_at,NOW()) WHERE id=$1`, eventID)
	return err
}

func (r *dynamic5hPolicyRepository) PruneMeterEvents(ctx context.Context, cutoff time.Time) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM dynamic_5h_meter_events WHERE id IN
(SELECT id FROM dynamic_5h_meter_events WHERE occurred_at<$1 AND delivered_at IS NOT NULL ORDER BY id LIMIT 1000)`, cutoff)
	return err
}
