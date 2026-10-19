package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/google/uuid"
)

type servicePoolQuotaReader interface {
	QueryUsage(context.Context, int64) (*OpenAIQuotaUsage, error)
}

type ServicePoolResetService struct {
	onboarding *OnboardingService
	quota      servicePoolQuotaReader
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	start      sync.Once
	stop       sync.Once
}

func NewServicePoolResetService(onboarding *OnboardingService, quota servicePoolQuotaReader) *ServicePoolResetService {
	ctx, cancel := context.WithCancel(context.Background())
	return &ServicePoolResetService{onboarding: onboarding, quota: quota, ctx: ctx, cancel: cancel}
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
						slog.Warn("service_pool_reset_scan_failed", "error", err)
					}
					cancel()
					timer.Reset(servicePoolResetInterval)
				}
			}
		}()
	})
}

func (s *ServicePoolResetService) Stop() {
	s.stop.Do(func() { s.cancel(); s.wg.Wait() })
}

func (s *ServicePoolResetService) RunOnce(ctx context.Context) error {
	onboarding := s.onboarding
	stored, err := onboarding.loadConfig(ctx)
	if err != nil {
		return err
	}
	if err := s.flushResetEffects(ctx); err != nil {
		return err
	}
	if !stored.PoolReset.Enabled {
		return nil
	}
	leaseRows, err := onboarding.db.QueryContext(ctx, `UPDATE service_pool_reset_state SET lease_until=NOW()+INTERVAL '5 minutes' WHERE id=1 AND lease_until<NOW() RETURNING id`)
	if err != nil {
		return err
	}
	acquired := leaseRows.Next()
	leaseErr := leaseRows.Err()
	_ = leaseRows.Close()
	if leaseErr != nil {
		return leaseErr
	}
	if !acquired {
		return nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = onboarding.db.ExecContext(cleanup, `UPDATE service_pool_reset_state SET lease_until=NOW()+INTERVAL '2 minutes' WHERE id=1`)
	}()
	accounts, err := onboarding.listOpenAIAccounts(ctx)
	if err != nil {
		return err
	}
	_, representatives := servicePoolResetCohort(accounts, time.Now())
	samples := s.queryPool(ctx, representatives)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.applyRound(ctx, samples, stored.PoolReset, time.Now()); err != nil {
		return err
	}
	return s.flushResetEffects(ctx)
}

func servicePoolResetCohort(accounts []Account, now time.Time) ([]string, map[string]int64) {
	representatives := make(map[string]int64)
	for _, account := range accounts {
		if !validOnboardingTemplate(&account) || (account.Status != StatusActive && account.Status != StatusError) || (account.ExpiresAt != nil && account.AutoPauseOnExpired && !account.ExpiresAt.After(now)) || onboardingPlanUnits(account.GetCredential("plan_type")) <= 0 {
			continue
		}
		accountID := strings.TrimSpace(account.GetCredential("chatgpt_account_id"))
		userID := strings.TrimSpace(account.GetCredential("chatgpt_user_id"))
		if userID == "" {
			userID = strings.ToLower(strings.TrimSpace(account.GetCredential("email")))
		}
		if accountID == "" || userID == "" {
			representatives[servicePoolResetHash(fmt.Sprintf("unidentified:%d", account.ID))] = 0
			continue
		}
		identity := servicePoolResetHash(accountID + ":" + userID)
		if current, exists := representatives[identity]; !exists || account.ID < current {
			representatives[identity] = account.ID
		}
	}
	cohort := make([]string, 0, len(representatives))
	for identity := range representatives {
		cohort = append(cohort, identity)
	}
	sort.Strings(cohort)
	return cohort, representatives
}

func (s *ServicePoolResetService) queryPool(ctx context.Context, representatives map[string]int64) map[string]servicePoolResetSample {
	type job struct {
		identity  string
		accountID int64
	}
	type result struct {
		identity string
		sample   servicePoolResetSample
	}
	jobs := make(chan job, len(representatives))
	results := make(chan result, len(representatives))
	for identity, accountID := range representatives {
		if accountID > 0 {
			jobs <- job{identity, accountID}
		}
	}
	close(jobs)
	var workers sync.WaitGroup
	for range min(8, len(representatives)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range jobs {
				if ctx.Err() != nil {
					return
				}
				queryCtx, cancel := context.WithTimeout(withOpenAIAutoResetContext(ctx), 20*time.Second)
				usage, err := s.quota.QueryUsage(queryCtx, item.accountID)
				cancel()
				if err == nil {
					if sample, valid := servicePoolResetSampleFromUsage(usage, time.Now()); valid {
						results <- result{item.identity, sample}
					}
				}
			}
		}()
	}
	workers.Wait()
	close(results)
	samples := make(map[string]servicePoolResetSample)
	for item := range results {
		samples[item.identity] = item.sample
	}
	return samples
}

func (s *ServicePoolResetService) applyRound(ctx context.Context, samples map[string]servicePoolResetSample, cfg ServicePoolResetConfig, now time.Time) error {
	tx, err := s.onboarding.db.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	client := tx.Client()
	rows, err := client.QueryContext(ctx, `SELECT state FROM service_pool_reset_state WHERE id=1 FOR UPDATE`)
	if err != nil {
		return err
	}
	var raw []byte
	if !rows.Next() {
		_ = rows.Close()
		return fmt.Errorf("pool reset state missing")
	}
	err = rows.Scan(&raw)
	_ = rows.Close()
	if err != nil {
		return err
	}
	var state servicePoolResetState
	if err := json.Unmarshal(raw, &state); err != nil {
		return err
	}
	current, err := s.onboarding.loadConfig(dbent.NewTxContext(ctx, tx))
	if err != nil {
		return err
	}
	if current.PoolReset != cfg || !current.PoolReset.Enabled {
		return nil
	}
	accounts, err := s.onboarding.listOpenAIAccounts(dbent.NewTxContext(ctx, tx))
	if err != nil {
		return err
	}
	cohort, _ := servicePoolResetCohort(accounts, now)
	decision := evaluateServicePoolReset(&state, cohort, samples, now)
	if decision != nil {
		if err := s.commitReset(ctx, client, decision, cfg, now); err != nil {
			return err
		}
		state.LastResetAt = now
		state.Pending = make(map[string]servicePoolResetEvidence)
		state.Status.PendingAccounts = 0
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if _, err := client.ExecContext(ctx, `UPDATE service_pool_reset_state SET state=$1::jsonb, updated_at=NOW() WHERE id=1`, payload); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *ServicePoolResetService) commitReset(ctx context.Context, client *dbent.Client, decision *servicePoolResetDecision, cfg ServicePoolResetConfig, now time.Time) error {
	id := strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	rows, err := client.QueryContext(ctx, `INSERT INTO service_pool_reset_events (id,event_key,kind,observed_accounts,total_accounts,reset_at,notification_channel_id,notification_content) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (event_key) DO NOTHING RETURNING id`, id, decision.Key, decision.Kind, decision.Observed, decision.Total, now, cfg.ChannelID, cfg.Message)
	if err != nil {
		return err
	}
	inserted := rows.Next()
	rowsErr := rows.Err()
	_ = rows.Close()
	if rowsErr != nil {
		return rowsErr
	}
	if !inserted {
		return nil
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

func (s *OnboardingService) poolResetStatus(ctx context.Context) (*ServicePoolResetStatus, error) {
	rows, err := s.client(ctx).QueryContext(ctx, `SELECT state FROM service_pool_reset_state WHERE id=1`)
	if err != nil {
		return nil, err
	}
	var raw []byte
	if !rows.Next() {
		rowsErr := rows.Err()
		_ = rows.Close()
		return nil, rowsErr
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
