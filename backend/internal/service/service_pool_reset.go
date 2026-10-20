package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/google/uuid"
)

type ServicePoolResetService struct {
	onboarding *ServiceCenterService
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	start      sync.Once
	stop       sync.Once
}

func NewServicePoolResetService(onboarding *ServiceCenterService) *ServicePoolResetService {
	ctx, cancel := context.WithCancel(context.Background())
	return &ServicePoolResetService{onboarding: onboarding, ctx: ctx, cancel: cancel}
}

func (s *ServicePoolResetService) Start() {
	s.start.Do(func() {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			timer := time.NewTimer(10 * time.Second)
			defer timer.Stop()
			for {
				select {
				case <-s.ctx.Done():
					return
				case <-timer.C:
					ctx, cancel := context.WithTimeout(s.ctx, 4*time.Minute)
					if err := s.RunOnce(ctx); err != nil && s.ctx.Err() == nil {
						slog.Warn("discord_reset_monitor_failed", "error", err)
					}
					cancel()
					timer.Reset(5 * time.Second)
				}
			}
		}()
	})
}

func (s *ServicePoolResetService) Stop() {
	s.stop.Do(func() { s.cancel(); s.wg.Wait() })
}

func (s *ServicePoolResetService) RunOnce(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	if err := s.flushResetEffects(ctx); err != nil {
		return err
	}
	config, err := s.onboarding.loadConfig(ctx)
	if err != nil || !config.PoolReset.Enabled {
		return err
	}
	stored, err := s.onboarding.loadResetMonitor(ctx)
	if err != nil {
		return err
	}
	leaseToken := uuid.NewString()
	leaseRows, err := s.onboarding.db.QueryContext(ctx, `UPDATE service_pool_reset_state SET lease_until=NOW()+INTERVAL '5 minutes',lease_token=$1 WHERE id=1 AND lease_until<NOW() AND next_check_at<=NOW() RETURNING id`, leaseToken)
	if err != nil {
		return err
	}
	acquired, leaseErr := leaseRows.Next(), leaseRows.Err()
	_ = leaseRows.Close()
	if leaseErr != nil || !acquired {
		return leaseErr
	}
	retryAfterSeconds := stored.Config.PollSeconds
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.onboarding.db.ExecContext(cleanup, `UPDATE service_pool_reset_state SET lease_until='-infinity',lease_token='',next_check_at=CASE WHEN (SELECT version FROM service_pool_reset_monitor WHERE id=1)=$3 THEN NOW()+($1 * INTERVAL '1 second') ELSE NOW() END WHERE id=1 AND lease_token=$2`, retryAfterSeconds, leaseToken, stored.Version)
	}()
	if err := s.resetMonitorRound(ctx, stored, leaseToken); err != nil {
		var rateLimit *resetMonitorRateLimitError
		if errors.As(err, &rateLimit) && int(rateLimit.RetryAfter.Seconds()) > retryAfterSeconds {
			retryAfterSeconds = int(rateLimit.RetryAfter.Seconds())
		}
		_ = s.resetMonitorStatus(ctx, "error", err.Error(), leaseToken)
		return err
	}
	return s.flushResetEffects(ctx)
}

func (s *ServicePoolResetService) commitReset(ctx context.Context, client *dbent.Client, decision *servicePoolResetDecision, cfg ServicePoolResetConfig, now time.Time) error {
	id := strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	rows, err := client.QueryContext(ctx, `INSERT INTO service_pool_reset_events (id,event_key,kind,observed_accounts,total_accounts,reset_at,notification_channel_id,notification_content) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (event_key) DO NOTHING RETURNING id`, id, decision.Key, decision.Kind, decision.Observed, decision.Total, now, cfg.ChannelID, cfg.Message)
	if err != nil {
		return err
	}
	inserted, rowsErr := rows.Next(), rows.Err()
	_ = rows.Close()
	if rowsErr != nil || !inserted {
		return rowsErr
	}
	_, err = client.ExecContext(ctx, `WITH reset AS (
UPDATE user_subscriptions SET daily_usage_usd=0, weekly_usage_usd=0, monthly_usage_usd=0,
daily_window_start=$2, weekly_window_start=$3, monthly_window_start=$3, updated_at=$3
WHERE deleted_at IS NULL AND expires_at>$3 AND starts_at<=$3 AND status IN ('active','suspended')
RETURNING user_id,group_id)
INSERT INTO service_pool_reset_cache_targets (event_id,user_id,group_id) SELECT $1,user_id,group_id FROM reset`, id, timezone.StartOfDay(now), now)
	if err != nil {
		return err
	}
	_, err = client.ExecContext(ctx, `UPDATE service_pool_reset_events SET affected_subscriptions=(SELECT COUNT(*) FROM service_pool_reset_cache_targets WHERE event_id=$1) WHERE id=$1`, id)
	return err
}

func (s *ServicePoolResetService) flushResetEffects(ctx context.Context) error {
	rows, err := s.onboarding.db.QueryContext(ctx, `SELECT event_id,user_id,group_id FROM service_pool_reset_cache_targets ORDER BY event_id,user_id,group_id LIMIT 1000`)
	if err != nil {
		return err
	}
	type target struct {
		event       string
		user, group int64
	}
	targets := make([]target, 0)
	for rows.Next() {
		var item target
		if err := rows.Scan(&item.event, &item.user, &item.group); err != nil {
			_ = rows.Close()
			return err
		}
		targets = append(targets, item)
	}
	rowsErr := rows.Err()
	_ = rows.Close()
	if rowsErr != nil {
		return rowsErr
	}
	for _, item := range targets {
		if s.onboarding.subscriptions == nil {
			return fmt.Errorf("subscription cache service unavailable")
		}
		if err := s.onboarding.subscriptions.invalidateSubscriptionCaches(item.user, item.group); err != nil {
			return err
		}
		if _, err := s.onboarding.db.ExecContext(ctx, `DELETE FROM service_pool_reset_cache_targets WHERE event_id=$1 AND user_id=$2 AND group_id=$3`, item.event, item.user, item.group); err != nil {
			return err
		}
	}
	return s.flushResetNotifications(ctx)
}

func (s *ServiceCenterService) poolResetStatus(ctx context.Context) (*ServicePoolResetStatus, error) {
	rows, err := s.client(ctx).QueryContext(ctx, `SELECT state FROM service_pool_reset_state WHERE id=1`)
	if err != nil {
		return nil, err
	}
	var raw []byte
	if !rows.Next() {
		_ = rows.Close()
		return &ServicePoolResetStatus{}, nil
	}
	err = rows.Scan(&raw)
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	var state servicePoolResetState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	status := state.Status
	rows, err = s.client(ctx).QueryContext(ctx, `SELECT reset_at,kind,affected_subscriptions,notification_state FROM service_pool_reset_events ORDER BY reset_at DESC LIMIT 1`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		var resetAt time.Time
		if err := rows.Scan(&resetAt, &status.LastResetKind, &status.AffectedSubscriptions, &status.NotificationState); err != nil {
			return nil, err
		}
		status.LastResetAt = &resetAt
	}
	return &status, rows.Err()
}
