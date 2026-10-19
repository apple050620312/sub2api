//go:build unit

package service

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func poolResetTestSamples(now time.Time, count int, used float64) ([]string, map[string]servicePoolResetSample) {
	cohort := make([]string, count)
	samples := make(map[string]servicePoolResetSample)
	for index := range cohort {
		identity := fmt.Sprintf("account-%d", index)
		cohort[index] = identity
		samples[identity] = servicePoolResetSample{At: now, Five: &OpenAIRateLimitWindow{UsedPercent: used, ResetAt: now.Add(3 * time.Hour).Unix()}, Week: &OpenAIRateLimitWindow{UsedPercent: used, ResetAt: now.Add(3 * 24 * time.Hour).Unix()}, CreditsKnown: true, Credits: map[string]string{}}
	}
	return cohort, samples
}

func TestServicePoolResetWindowsRequiresQuorumAndTwoFreshRounds(t *testing.T) {
	now := time.Now().UTC()
	state := &servicePoolResetState{}
	cohort, samples := poolResetTestSamples(now, 10, 40)
	require.Nil(t, evaluateServicePoolReset(state, cohort, samples, now))
	cohort, samples = poolResetTestSamples(now.Add(2*time.Minute), 10, 0)
	require.Nil(t, evaluateServicePoolReset(state, cohort, samples, now.Add(2*time.Minute)))
	require.Len(t, state.Pending, 10)
	encoded, err := json.Marshal(state)
	require.NoError(t, err)
	var restarted servicePoolResetState
	require.NoError(t, json.Unmarshal(encoded, &restarted))
	cohort, samples = poolResetTestSamples(now.Add(4*time.Minute), 10, 1)
	delete(samples, "account-9")
	decision := evaluateServicePoolReset(&restarted, cohort, samples, now.Add(4*time.Minute))
	require.NotNil(t, decision)
	require.Equal(t, "windows", decision.Kind)
	require.Equal(t, 9, decision.Observed)
	require.Equal(t, 10, decision.Total)
	restarted.LastResetAt = now.Add(4 * time.Minute)
	restarted.Pending = make(map[string]servicePoolResetEvidence)
	cohort, samples = poolResetTestSamples(now.Add(6*time.Minute), 10, 0)
	require.Nil(t, evaluateServicePoolReset(&restarted, cohort, samples, now.Add(6*time.Minute)))
}

func TestServicePoolResetRejectsNaturalExpiryMissingDataConsumptionAndStaleSamples(t *testing.T) {
	for _, scenario := range []string{"five_expired", "week_expired", "only_five_reset", "missing_week", "unknown_credits", "consumed_credit", "missing_snapshots", "stale_baseline", "cohort_changed", "too_few_accounts", "old_round", "tiny_usage"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC()
			state := &servicePoolResetState{}
			cohort, before := poolResetTestSamples(now, 10, 40)
			if scenario == "too_few_accounts" {
				cohort, before = poolResetTestSamples(now, 2, 40)
			}
			for identity, sample := range before {
				switch scenario {
				case "five_expired":
					sample.Five.ResetAt = now.Add(time.Minute).Unix()
				case "week_expired":
					sample.Week.ResetAt = now.Add(time.Minute).Unix()
				case "consumed_credit":
					sample.Credits["credit"] = "expires"
					sample.CreditCount = 1
				case "tiny_usage":
					sample.Week.UsedPercent = 1
				}
				before[identity] = sample
			}
			require.Nil(t, evaluateServicePoolReset(state, cohort, before, now))
			at := now.Add(2 * time.Minute)
			if scenario == "stale_baseline" {
				at = now.Add(time.Hour)
			}
			if scenario == "old_round" {
				at = now.Add(-time.Minute)
			}
			_, after := poolResetTestSamples(at, len(cohort), 0)
			for identity, sample := range after {
				switch scenario {
				case "only_five_reset":
					sample.Week.UsedPercent = 40
				case "missing_week":
					sample.Week = nil
				case "unknown_credits":
					sample.CreditsKnown = false
				}
				after[identity] = sample
			}
			if scenario == "missing_snapshots" {
				delete(after, "account-8")
				delete(after, "account-9")
			}
			if scenario == "cohort_changed" {
				cohort = append(cohort, "new-account")
			}
			require.Nil(t, evaluateServicePoolReset(state, cohort, after, at))
			for identity, sample := range after {
				sample.At = at.Add(2 * time.Minute)
				after[identity] = sample
			}
			require.Nil(t, evaluateServicePoolReset(state, cohort, after, at.Add(2*time.Minute)))
		})
	}
}

func TestServicePoolResetFifteenMinutePollingConfirmsWindowReset(t *testing.T) {
	now := time.Now().UTC()
	state := &servicePoolResetState{}
	cohort, samples := poolResetTestSamples(now, 8, 40)
	require.Nil(t, evaluateServicePoolReset(state, cohort, samples, now))
	for round := 1; round <= 2; round++ {
		at := now.Add(time.Duration(round) * 15 * time.Minute)
		_, samples = poolResetTestSamples(at, 8, 0)
		decision := evaluateServicePoolReset(state, cohort, samples, at)
		if round == 1 {
			require.Nil(t, decision)
			require.Len(t, state.Pending, 8)
		} else {
			require.NotNil(t, decision)
			require.Equal(t, "windows", decision.Kind)
		}
	}
}

func TestServicePoolResetWaitsForEightAccountVoucherPropagation(t *testing.T) {
	now := time.Now().UTC()
	state := &servicePoolResetState{}
	cohort, samples := poolResetTestSamples(now, 8, 40)
	require.Nil(t, evaluateServicePoolReset(state, cohort, samples, now))
	batch := now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
	for round, granted := range []int{4, 7, 8, 8} {
		at := now.Add(time.Duration(round+1) * 15 * time.Minute)
		_, samples = poolResetTestSamples(at, 8, 40)
		for index := range granted {
			identity := cohort[index]
			sample := samples[identity]
			sample.Credits = map[string]string{identity: batch}
			sample.CreditCount = 1
			samples[identity] = sample
		}
		decision := evaluateServicePoolReset(state, cohort, samples, at)
		if round < 3 {
			require.Nil(t, decision)
		} else {
			require.NotNil(t, decision)
			require.Equal(t, "credits", decision.Kind)
			require.Equal(t, 8, decision.Observed)
		}
	}
}

func TestServicePoolResetOnlyNewSameBatchVouchersTrigger(t *testing.T) {
	for _, scenario := range []string{"same_batch", "existing_on_startup", "mixed_batches", "unknown_previous", "count_decreased", "not_confirmed"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC()
			state := &servicePoolResetState{}
			cohort, before := poolResetTestSamples(now, 10, 40)
			batch := now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
			for identity, sample := range before {
				if scenario == "existing_on_startup" || scenario == "count_decreased" {
					sample.Credits[identity] = batch
					sample.CreditCount = 1
				}
				if scenario == "unknown_previous" {
					sample.CreditsKnown = false
				}
				before[identity] = sample
			}
			require.Nil(t, evaluateServicePoolReset(state, cohort, before, now))
			_, after := poolResetTestSamples(now.Add(2*time.Minute), 10, 40)
			for identity, sample := range after {
				sample.Credits[identity] = batch
				sample.CreditCount = 1
				if scenario == "mixed_batches" {
					sample.Credits[identity] = identity
				}
				if scenario == "count_decreased" {
					sample.Credits = map[string]string{}
					sample.CreditCount = 0
				}
				after[identity] = sample
			}
			require.Nil(t, evaluateServicePoolReset(state, cohort, after, now.Add(2*time.Minute)))
			for identity, sample := range after {
				sample.At = now.Add(4 * time.Minute)
				if scenario == "not_confirmed" {
					sample.Credits = nil
					sample.CreditCount = 0
				}
				after[identity] = sample
			}
			decision := evaluateServicePoolReset(state, cohort, after, now.Add(4*time.Minute))
			if scenario == "same_batch" {
				require.NotNil(t, decision)
				require.Equal(t, "credits", decision.Kind)
			} else {
				require.Nil(t, decision)
			}
		})
	}
}

func TestServicePoolResetCoalescesDelayedVouchersAfterWindowReset(t *testing.T) {
	now := time.Now().UTC()
	state := &servicePoolResetState{LastResetAt: now}
	cohort, samples := poolResetTestSamples(now, 8, 0)
	require.Nil(t, evaluateServicePoolReset(state, cohort, samples, now))
	batch := now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
	for round := 1; round <= 2; round++ {
		at := now.Add(time.Duration(round) * 15 * time.Minute)
		_, samples = poolResetTestSamples(at, 8, 0)
		for identity, sample := range samples {
			sample.Credits = map[string]string{identity: batch}
			sample.CreditCount = 1
			samples[identity] = sample
		}
		require.Nil(t, evaluateServicePoolReset(state, cohort, samples, at))
	}
	require.Empty(t, state.Pending)
}

func TestServicePoolResetRejectsExpiredPropagationEvidence(t *testing.T) {
	now := time.Now().UTC()
	state := &servicePoolResetState{}
	cohort, samples := poolResetTestSamples(now, 8, 40)
	require.Nil(t, evaluateServicePoolReset(state, cohort, samples, now))
	batch := now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
	for round := 1; round <= 7; round++ {
		at := now.Add(time.Duration(round) * 15 * time.Minute)
		_, samples = poolResetTestSamples(at, 8, 40)
		granted := 4
		if round >= 6 {
			granted = 8
		}
		for index := range granted {
			identity := cohort[index]
			sample := samples[identity]
			sample.Credits = map[string]string{identity: batch}
			sample.CreditCount = 1
			samples[identity] = sample
		}
		require.Nil(t, evaluateServicePoolReset(state, cohort, samples, at))
	}
}

func TestServicePoolResetCohortDeduplicatesRealIdentitiesAndShadows(t *testing.T) {
	parent := int64(1)
	base := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Credentials: map[string]any{"chatgpt_account_id": "gpt-account", "chatgpt_user_id": "gpt-user", "plan_type": "plus"}}
	duplicate, shadow := base, base
	duplicate.ID = 2
	shadow.ID, shadow.ParentAccountID = 3, &parent
	inactive := base
	inactive.ID, inactive.Status = 4, "inactive"
	inactive.Credentials = map[string]any{"chatgpt_account_id": "inactive-account", "chatgpt_user_id": "inactive-user", "plan_type": "plus"}
	cohort, representatives := servicePoolResetCohort([]Account{base, duplicate, shadow, inactive}, time.Now())
	require.Len(t, cohort, 1)
	require.Equal(t, int64(1), representatives[cohort[0]])
}

func TestServicePoolResetReappearingKnownVouchersAreNotNewGrants(t *testing.T) {
	now := time.Now().UTC()
	state := &servicePoolResetState{}
	cohort, before := poolResetTestSamples(now, 3, 40)
	for identity, sample := range before {
		sample.Credits[identity] = now.Add(24 * time.Hour).Format(time.RFC3339)
		sample.CreditCount = 1
		before[identity] = sample
	}
	require.Nil(t, evaluateServicePoolReset(state, cohort, before, now))
	_, consumed := poolResetTestSamples(now.Add(2*time.Minute), 3, 40)
	require.Nil(t, evaluateServicePoolReset(state, cohort, consumed, now.Add(2*time.Minute)))
	for identity, sample := range before {
		sample.At = now.Add(4 * time.Minute)
		before[identity] = sample
	}
	require.Nil(t, evaluateServicePoolReset(state, cohort, before, now.Add(4*time.Minute)))
	for identity, sample := range before {
		sample.At = now.Add(6 * time.Minute)
		before[identity] = sample
	}
	require.Nil(t, evaluateServicePoolReset(state, cohort, before, now.Add(6*time.Minute)))
	require.Empty(t, state.Pending)
}

func TestServicePoolResetSampleRejectsUnknownAndStaleVoucherData(t *testing.T) {
	now := time.Now().UTC()
	usage := &OpenAIQuotaUsage{FetchedAt: now.Unix(), RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 1}, ResetCreditDetailsComplete: true}
	_, valid := servicePoolResetSampleFromUsage(usage, now)
	require.False(t, valid, "count without complete identified vouchers is not evidence")
	usage.autoResetCandidates = []openAIAutoResetCreditCandidate{{ID: "private-credit-id", ExpiresAt: now.Add(time.Hour).Format(time.RFC3339)}}
	sample, valid := servicePoolResetSampleFromUsage(usage, now)
	require.True(t, valid)
	require.True(t, sample.CreditsKnown)
	payload, err := json.Marshal(sample)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "private-credit-id")
	_, valid = servicePoolResetSampleFromUsage(usage, now.Add(2*time.Minute))
	require.False(t, valid)
	public, err := json.Marshal(usage)
	require.NoError(t, err)
	require.NotContains(t, string(public), "ResetCreditDetailsComplete")
	require.NotContains(t, string(public), "private-credit-id")
}

func TestServicePoolResetCoalescedCampaignCannotTriggerAfterCooldown(t *testing.T) {
	now := time.Now().UTC()
	state := &servicePoolResetState{LastResetAt: now}
	cohort, samples := poolResetTestSamples(now.Add(2*time.Minute), 3, 0)
	require.Nil(t, evaluateServicePoolReset(state, cohort, samples, now.Add(2*time.Minute)))
	for identity, sample := range samples {
		sample.At = now.Add(4 * time.Minute)
		sample.CreditCount = 1
		sample.Credits = map[string]string{identity: now.Add(24 * time.Hour).Format(time.RFC3339)}
		samples[identity] = sample
	}
	require.Nil(t, evaluateServicePoolReset(state, cohort, samples, now.Add(4*time.Minute)))
	require.Len(t, state.Pending, 3)
	for identity, sample := range samples {
		sample.At = now.Add(6 * time.Minute)
		samples[identity] = sample
	}
	require.Nil(t, evaluateServicePoolReset(state, cohort, samples, now.Add(6*time.Minute)))
	require.Empty(t, state.Pending)
	for identity, sample := range samples {
		sample.At = now.Add(12 * time.Minute)
		samples[identity] = sample
	}
	require.Nil(t, evaluateServicePoolReset(state, cohort, samples, now.Add(12*time.Minute)))
}

func TestServicePoolResetExplicitZeroCreditsIsKnownButPartialDataIsNot(t *testing.T) {
	for _, scenario := range []struct {
		body    string
		known   bool
		invalid bool
	}{
		{`{"available_count":0}`, true, false},
		{`[]`, true, false},
		{`{"available_count":1}`, false, false},
		{`{}`, false, false},
		{`{"available_count":0,"credits":"invalid"}`, false, true},
	} {
		t.Run(scenario.body, func(t *testing.T) {
			details, err := parseOpenAIRateLimitResetCreditDetails([]byte(scenario.body))
			if scenario.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, scenario.known, details.ObservationComplete)
		})
	}
}
