package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

type gmailResetPending struct {
	Key        string    `json:"key"`
	Kind       string    `json:"kind"`
	ReceivedAt time.Time `json:"received_at"`
	ApplyAt    time.Time `json:"apply_at"`
	Count      int       `json:"count"`
}

type gmailResetMonitorState struct {
	Version           int64              `json:"version"`
	Since             time.Time          `json:"since"`
	Pending           *gmailResetPending `json:"pending,omitempty"`
	LastEventReceived time.Time          `json:"last_event_received"`
	CooldownUntil     time.Time          `json:"cooldown_until"`
}

func acceptGmailResetMail(state *servicePoolResetState, record *gmailResetMailRecord, cfg GmailResetConfig, now time.Time) {
	if !(record.Confidence >= cfg.MinConfidence) || (record.Kind != "direct_reset" && record.Kind != "banked_reset") {
		record.Outcome = "ignored"
		return
	}
	if (record.Kind == "direct_reset" && !cfg.DirectResetEnabled) || (record.Kind == "banked_reset" && !cfg.BankedResetEnabled) {
		record.Outcome = "kind_disabled"
		return
	}
	monitor := &state.Gmail
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
	monitor.Pending = &gmailResetPending{Key: record.Key, Kind: record.Kind, ReceivedAt: record.ReceivedAt, ApplyAt: now.Add(time.Duration(cfg.PropagationMinutes) * time.Minute), Count: 1}
	record.Outcome = "pending"
}

func readyGmailReset(state *servicePoolResetState, cfg GmailResetConfig, now time.Time, backlog bool) *servicePoolResetDecision {
	monitor := &state.Gmail
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
	return &servicePoolResetDecision{Key: "gmail:" + pending.Key, Kind: "gmail_" + pending.Kind, Observed: pending.Count, Total: 1}
}

func (s *ServicePoolResetService) gmailResetRound(ctx context.Context, stored *gmailResetStored, leaseToken string) error {
	if stored.RefreshEncrypted == "" {
		return s.gmailResetStatus(ctx, "gmail_disconnected", "", leaseToken)
	}
	if stored.Config.ModelBaseURL == "" || stored.Config.Model == "" || stored.ModelKeyEncrypted == "" {
		return s.gmailResetStatus(ctx, "model_not_configured", "", leaseToken)
	}
	if err := validateGmailResetConfig(stored.Config); err != nil {
		return err
	}
	credentials, err := s.onboarding.gmailResetCredentials(stored)
	if err != nil {
		return err
	}
	refresh, err := s.onboarding.encryptor.Decrypt(stored.RefreshEncrypted)
	if err != nil {
		return fmt.Errorf("无法解密 Gmail 刷新令牌，请检查持久化加密密钥")
	}
	key, err := s.onboarding.encryptor.Decrypt(stored.ModelKeyEncrypted)
	if err != nil {
		return fmt.Errorf("无法解密邮件分析模型密钥，请检查持久化加密密钥")
	}
	client := gmailResetHTTPClient(30 * time.Second)
	token, err := gmailResetExchangeToken(ctx, client, url.Values{"client_id": {credentials.ClientID}, "client_secret": {credentials.ClientSecret}, "refresh_token": {refresh}, "grant_type": {"refresh_token"}})
	if err != nil {
		return err
	}
	ids, err := gmailResetList(ctx, client, token.AccessToken, gmailResetQuery(stored, time.Now()))
	if err != nil {
		return err
	}
	records := make([]gmailResetMailRecord, 0)
	backlog := false
	var processingError error
	for index := len(ids) - 1; index >= 0; index-- {
		messageKey := servicePoolResetHash(stored.Email + ":" + ids[index])
		attempts, seen, err := s.gmailResetProgress(ctx, messageKey)
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
		var message gmailResetMessage
		if err := gmailResetGet(ctx, client, token.AccessToken, "https://gmail.googleapis.com/gmail/v1/users/me/messages/"+url.PathEscape(ids[index])+"?format=full", &message); err != nil {
			processingError, backlog = err, true
			break
		}
		received, err := strconv.ParseInt(message.InternalDate, 10, 64)
		if err != nil {
			processingError = fmt.Errorf("Gmail 邮件时间戳无效，已跳过")
			records = append(records, gmailResetMailRecord{Key: messageKey, ReceivedAt: time.Now(), Kind: "unknown", Outcome: "invalid_message"})
			continue
		}
		sender, trusted := gmailResetSenderAllowed(message.Payload, stored.Config)
		record := gmailResetMailRecord{Key: messageKey, ReceivedAt: time.UnixMilli(received), Sender: sender, Subject: gmailResetTruncate(gmailResetHeader(message.Payload, "Subject"), 500), Kind: "unknown", Outcome: "untrusted_sender"}
		now := time.Now()
		if !record.ReceivedAt.After(stored.Since) || now.Sub(record.ReceivedAt) > time.Duration(stored.Config.MaxAgeHours)*time.Hour || record.ReceivedAt.After(now.Add(5*time.Minute)) {
			record.Outcome = "stale"
		} else if trusted {
			record.Body = gmailResetTruncate(gmailResetBody(message.Payload, 0), stored.Config.MaxBodyChars)
			if record.Body == "" {
				record.Outcome = "empty_body"
			} else {
				record.Attempts = attempts + 1
				analysisCtx, cancel := context.WithTimeout(ctx, time.Duration(stored.Config.ModelTimeoutSeconds)*time.Second)
				analysis, err := gmailResetAnalyze(analysisCtx, gmailResetHTTPClient(time.Duration(stored.Config.ModelTimeoutSeconds)*time.Second), stored.Config, key, record)
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
		}
		records = append(records, record)
	}
	return s.applyGmailResetRound(ctx, stored, records, backlog, processingError, leaseToken)
}

func (s *ServicePoolResetService) gmailResetProgress(ctx context.Context, key string) (int, bool, error) {
	rows, err := s.onboarding.db.QueryContext(ctx, `SELECT attempts,outcome FROM service_pool_reset_mail WHERE message_key=$1`, key)
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

func (s *ServicePoolResetService) applyGmailResetRound(ctx context.Context, stored *gmailResetStored, records []gmailResetMailRecord, backlog bool, processingError error, leaseToken string) error {
	tx, err := s.onboarding.db.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	client := tx.Client()
	txCtx := dbent.NewTxContext(ctx, tx)
	rows, err := client.QueryContext(ctx, `SELECT version FROM service_pool_reset_gmail WHERE id=1 FOR UPDATE`)
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
		return fmt.Errorf("Gmail 监测锁已到期，等待下一轮重新检查")
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
	if !state.Gmail.Since.Equal(stored.Since) || state.Gmail.Version != stored.Version {
		state.Gmail.Since, state.Gmail.Version, state.Gmail.Pending = stored.Since, stored.Version, nil
	}
	now := time.Now()
	if state.Gmail.CooldownUntil.IsZero() && !state.LastResetAt.IsZero() {
		state.Gmail.CooldownUntil = state.LastResetAt.Add(time.Duration(stored.Config.CooldownMinutes) * time.Minute)
	}
	sort.SliceStable(records, func(first, second int) bool { return records[first].ReceivedAt.Before(records[second].ReceivedAt) })
	for _, record := range records {
		if record.Outcome == "analyzed" {
			previous := state.Gmail.Pending
			acceptGmailResetMail(&state, &record, stored.Config, now)
			if previous != nil && state.Gmail.Pending != nil && previous.Key != state.Gmail.Pending.Key {
				if _, err := client.ExecContext(ctx, `UPDATE service_pool_reset_mail SET outcome='superseded' WHERE message_key=$1`, previous.Key); err != nil {
					return err
				}
			}
		}
		if _, err := client.ExecContext(ctx, `INSERT INTO service_pool_reset_mail (message_key,received_at,sender,subject,kind,confidence,evidence,outcome,attempts) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (message_key) DO UPDATE SET kind=EXCLUDED.kind,confidence=EXCLUDED.confidence,evidence=EXCLUDED.evidence,outcome=EXCLUDED.outcome,attempts=EXCLUDED.attempts WHERE service_pool_reset_mail.outcome='analysis_retry'`, record.Key, record.ReceivedAt, record.Sender, record.Subject, record.Kind, record.Confidence, record.Evidence, record.Outcome, record.Attempts); err != nil {
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
	if decision := readyGmailReset(&state, stored.Config, now, backlog); decision != nil {
		if err := s.commitReset(ctx, client, decision, current.PoolReset, now); err != nil {
			return err
		}
		state.LastResetAt = now
		if _, err := client.ExecContext(ctx, `UPDATE service_pool_reset_mail SET outcome='applied' WHERE message_key=$1`, state.Gmail.Pending.Key); err != nil {
			return err
		}
		state.Gmail.LastEventReceived = state.Gmail.Pending.ReceivedAt
		state.Gmail.CooldownUntil = now.Add(time.Duration(stored.Config.CooldownMinutes) * time.Minute)
		state.Gmail.Pending = nil
	}
	state.Status.CheckedAt = &now
	state.Status.PendingUntil, state.Status.CooldownUntil = nil, nil
	if state.Gmail.Pending != nil {
		state.Status.PendingUntil = &state.Gmail.Pending.ApplyAt
		if !backlog && processingError == nil {
			state.Status.Phase = "propagating"
		}
	} else if now.Before(state.Gmail.CooldownUntil) {
		state.Status.CooldownUntil = &state.Gmail.CooldownUntil
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
	if _, err := client.ExecContext(ctx, `DELETE FROM service_pool_reset_mail WHERE created_at<NOW()-($1 * INTERVAL '1 day')`, stored.Config.RetentionDays); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *ServicePoolResetService) gmailResetStatus(ctx context.Context, phase, errorText, leaseToken string) error {
	payload, err := json.Marshal(map[string]any{"phase": phase, "last_error": errorText, "checked_at": time.Now()})
	if err != nil {
		return err
	}
	_, err = s.onboarding.db.ExecContext(ctx, `UPDATE service_pool_reset_state SET state=jsonb_set(state,'{status}',COALESCE(state->'status','{}'::jsonb)||$1::jsonb),updated_at=NOW() WHERE id=1 AND lease_token=$2`, payload, leaseToken)
	return err
}
