package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

type resetMonitorPending struct {
	Key        string    `json:"key"`
	Kind       string    `json:"kind"`
	ReceivedAt time.Time `json:"received_at"`
	ApplyAt    time.Time `json:"apply_at"`
	Count      int       `json:"count"`
}

type resetMonitorState struct {
	Version           int64                `json:"version"`
	Since             time.Time            `json:"since"`
	Pending           *resetMonitorPending `json:"pending,omitempty"`
	LastEventReceived time.Time            `json:"last_event_received"`
	CooldownUntil     time.Time            `json:"cooldown_until"`
}

func acceptResetMessage(state *servicePoolResetState, record *resetMonitorMessageRecord, cfg ResetMonitorConfig, now time.Time) {
	if !(record.Confidence >= cfg.MinConfidence) || (record.Kind != "direct_reset" && record.Kind != "banked_reset") {
		record.Outcome = "ignored"
		return
	}
	if (record.Kind == "direct_reset" && !cfg.DirectResetEnabled) || (record.Kind == "banked_reset" && !cfg.BankedResetEnabled) {
		record.Outcome = "kind_disabled"
		return
	}
	monitor := &state.Monitor
	aggregation := time.Duration(cfg.AggregationMinutes) * time.Minute
	if !monitor.LastEventReceived.IsZero() && !record.ReceivedAt.After(monitor.LastEventReceived.Add(aggregation)) {
		record.Outcome = "merged"
		return
	}
	if now.Before(monitor.CooldownUntil) || record.ReceivedAt.Before(monitor.CooldownUntil) {
		record.Outcome = "cooldown"
		return
	}
	if monitor.Pending != nil {
		if record.ReceivedAt.Before(monitor.Pending.ReceivedAt.Add(-aggregation)) {
			record.Outcome = "superseded"
			return
		}
		if !record.ReceivedAt.After(monitor.Pending.ReceivedAt.Add(aggregation)) {
			monitor.Pending.Count++
			record.Outcome = "merged"
			return
		}
	}
	monitor.Pending = &resetMonitorPending{Key: record.Key, Kind: record.Kind, ReceivedAt: record.ReceivedAt, ApplyAt: now.Add(time.Duration(cfg.PropagationMinutes) * time.Minute), Count: 1}
	record.Outcome = "pending"
}

func readyResetMonitor(state *servicePoolResetState, cfg ResetMonitorConfig, now time.Time, backlog bool) *servicePoolResetDecision {
	monitor := &state.Monitor
	pending := monitor.Pending
	if pending == nil {
		return nil
	}
	if now.Sub(pending.ReceivedAt) > time.Duration(cfg.MaxAgeHours)*time.Hour || (pending.Kind == "direct_reset" && !cfg.DirectResetEnabled) || (pending.Kind == "banked_reset" && !cfg.BankedResetEnabled) {
		monitor.Pending = nil
		return nil
	}
	if backlog || now.Before(pending.ApplyAt) || now.Before(monitor.CooldownUntil) {
		return nil
	}
	return &servicePoolResetDecision{Key: "discord:" + pending.Key, Kind: "discord_" + pending.Kind, Observed: pending.Count, Total: 1}
}

func (s *ServicePoolResetService) resetMonitorRound(ctx context.Context, stored *resetMonitorStored, leaseToken string) error {
	global, err := s.onboarding.loadConfig(ctx)
	if err != nil {
		return err
	}
	if global.Discord.BotToken == "" || global.Discord.GuildID == "" {
		return s.resetMonitorStatus(ctx, "discord_not_configured", "", leaseToken)
	}
	if stored.Config.ModelAPIKeyID <= 0 || stored.OwnerUserID <= 0 {
		return s.resetMonitorStatus(ctx, "model_not_configured", "", leaseToken)
	}
	if err := validateResetMonitorConfig(stored.Config); err != nil {
		return err
	}
	messages, err := resetMonitorMessages(ctx, resetMonitorHTTPClient(30*time.Second, false), global.Discord.BotToken, global.Discord.GuildID, stored, time.Now())
	if err != nil {
		return err
	}
	records := make([]resetMonitorMessageRecord, 0)
	backlog := false
	var processingError error
	for index := len(messages) - 1; index >= 0; index-- {
		record, allowed := resetMonitorRecord(messages[index], stored.Config)
		if !allowed {
			continue
		}
		attempts, seen, err := s.resetMonitorProgress(ctx, record.Key)
		if err != nil {
			return err
		}
		if seen {
			continue
		}
		deadline, hasDeadline := ctx.Deadline()
		if len(records) >= stored.Config.BatchSize || (hasDeadline && time.Until(deadline) < time.Duration(stored.Config.ModelTimeoutSeconds+15)*time.Second) {
			backlog = true
			break
		}
		now := time.Now()
		if !record.ReceivedAt.After(stored.Since) || now.Sub(record.ReceivedAt) > time.Duration(stored.Config.MaxAgeHours)*time.Hour || record.ReceivedAt.After(now.Add(5*time.Minute)) {
			record.Outcome = "stale"
		} else {
			record.Attempts = attempts + 1
			analysisCtx, cancel := context.WithTimeout(ctx, time.Duration(stored.Config.ModelTimeoutSeconds)*time.Second)
			analysis, err := s.analyzeResetMessage(analysisCtx, stored, record)
			cancel()
			if err != nil {
				processingError = err
				record.Outcome = "analysis_failed"
				if record.Attempts < stored.Config.MaxModelAttempts {
					record.Outcome, backlog = "analysis_retry", true
				}
			} else {
				record.Kind, record.Confidence, record.Evidence, record.Outcome = analysis.Kind, analysis.Confidence, analysis.Evidence, "analyzed"
			}
		}
		records = append(records, record)
	}
	return s.applyResetMonitorRound(ctx, stored, records, backlog, processingError, leaseToken)
}

func (s *ServicePoolResetService) resetMonitorProgress(ctx context.Context, key string) (int, bool, error) {
	rows, err := s.onboarding.db.QueryContext(ctx, `SELECT attempts,outcome FROM service_pool_reset_messages WHERE message_key=$1`, key)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return 0, false, rows.Err()
	}
	var attempts int
	var outcome string
	if err := rows.Scan(&attempts, &outcome); err != nil {
		return 0, false, err
	}
	return attempts, outcome != "analysis_retry", rows.Err()
}

func (s *ServicePoolResetService) applyResetMonitorRound(ctx context.Context, stored *resetMonitorStored, records []resetMonitorMessageRecord, backlog bool, processingError error, leaseToken string) error {
	tx, err := s.onboarding.db.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	client := tx.Client()
	txCtx := dbent.NewTxContext(ctx, tx)
	rows, err := client.QueryContext(ctx, `SELECT version FROM service_pool_reset_monitor WHERE id=1 FOR UPDATE`)
	if err != nil {
		return err
	}
	var version int64
	if rows.Next() {
		err = rows.Scan(&version)
	}
	rowsErr := rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	if rowsErr != nil {
		return rowsErr
	}
	if version != stored.Version {
		return nil
	}
	current, err := s.onboarding.loadConfig(txCtx)
	if err != nil {
		return err
	}
	if !current.PoolReset.Enabled {
		return nil
	}
	rows, err = client.QueryContext(ctx, `SELECT state FROM service_pool_reset_state WHERE id=1 AND lease_until>NOW() AND lease_token=$1 FOR UPDATE`, leaseToken)
	if err != nil {
		return err
	}
	var raw []byte
	if !rows.Next() {
		_ = rows.Close()
		return fmt.Errorf("重置监测锁已到期，等待下一轮重新检查")
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
	if !state.Monitor.Since.Equal(stored.Since) || state.Monitor.Version != stored.Version {
		state.Monitor.Since, state.Monitor.Version, state.Monitor.Pending = stored.Since, stored.Version, nil
	}
	now := time.Now()
	if state.Monitor.CooldownUntil.IsZero() && !state.LastResetAt.IsZero() {
		state.Monitor.CooldownUntil = state.LastResetAt.Add(time.Duration(stored.Config.CooldownMinutes) * time.Minute)
	}
	sort.SliceStable(records, func(first, second int) bool { return records[first].ReceivedAt.Before(records[second].ReceivedAt) })
	for _, record := range records {
		if record.Outcome == "analyzed" {
			previous := state.Monitor.Pending
			acceptResetMessage(&state, &record, stored.Config, now)
			if previous != nil && state.Monitor.Pending != nil && previous.Key != state.Monitor.Pending.Key {
				if _, err := client.ExecContext(ctx, `UPDATE service_pool_reset_messages SET outcome='superseded' WHERE message_key=$1`, previous.Key); err != nil {
					return err
				}
			}
		}
		if _, err := client.ExecContext(ctx, `INSERT INTO service_pool_reset_messages (message_key,message_id,received_at,sender,subject,kind,confidence,evidence,outcome,attempts) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT (message_key) DO UPDATE SET kind=EXCLUDED.kind,confidence=EXCLUDED.confidence,evidence=EXCLUDED.evidence,outcome=EXCLUDED.outcome,attempts=EXCLUDED.attempts WHERE service_pool_reset_messages.outcome='analysis_retry'`, record.Key, record.MessageID, record.ReceivedAt, record.Sender, record.Subject, record.Kind, record.Confidence, record.Evidence, record.Outcome, record.Attempts); err != nil {
			return err
		}
	}
	state.Status.Phase, state.Status.LastError = "monitoring", ""
	if backlog {
		state.Status.Phase = "backlog"
	}
	if processingError != nil {
		state.Status.Phase, state.Status.LastError = "error", processingError.Error()
	}
	if decision := readyResetMonitor(&state, stored.Config, now, backlog); decision != nil {
		if err := s.commitReset(ctx, client, decision, current.PoolReset, now); err != nil {
			return err
		}
		state.LastResetAt = now
		if _, err := client.ExecContext(ctx, `UPDATE service_pool_reset_messages SET outcome='applied' WHERE message_key=$1`, state.Monitor.Pending.Key); err != nil {
			return err
		}
		state.Monitor.LastEventReceived = state.Monitor.Pending.ReceivedAt
		state.Monitor.CooldownUntil = now.Add(time.Duration(stored.Config.CooldownMinutes) * time.Minute)
		state.Monitor.Pending = nil
	}
	state.Status.CheckedAt = &now
	state.Status.PendingUntil, state.Status.CooldownUntil = nil, nil
	if state.Monitor.Pending != nil {
		state.Status.PendingUntil = &state.Monitor.Pending.ApplyAt
		if !backlog && processingError == nil {
			state.Status.Phase = "propagating"
		}
	} else if now.Before(state.Monitor.CooldownUntil) {
		state.Status.CooldownUntil = &state.Monitor.CooldownUntil
		if !backlog && processingError == nil {
			state.Status.Phase = "cooldown"
		}
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if _, err := client.ExecContext(ctx, `UPDATE service_pool_reset_state SET state=$1::jsonb,updated_at=NOW() WHERE id=1`, payload); err != nil {
		return err
	}
	if _, err := client.ExecContext(ctx, `DELETE FROM service_pool_reset_messages WHERE created_at<NOW()-($1 * INTERVAL '1 day')`, stored.Config.RetentionDays); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *ServicePoolResetService) resetMonitorStatus(ctx context.Context, phase, errorText, leaseToken string) error {
	payload, err := json.Marshal(map[string]any{"phase": phase, "last_error": errorText, "checked_at": time.Now()})
	if err != nil {
		return err
	}
	_, err = s.onboarding.db.ExecContext(ctx, `UPDATE service_pool_reset_state SET state=jsonb_set(state,'{status}',COALESCE(state->'status','{}'::jsonb)||$1::jsonb),updated_at=NOW() WHERE id=1 AND lease_token=$2`, payload, leaseToken)
	return err
}
