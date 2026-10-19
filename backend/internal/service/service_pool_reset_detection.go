package service

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	servicePoolResetInterval     = 15 * time.Minute
	servicePoolResetMaxSampleGap = 45 * time.Minute
	servicePoolResetEvidenceTTL  = time.Hour
)

type ServicePoolResetConfig struct {
	Enabled   bool   `json:"enabled"`
	ChannelID string `json:"channel_id"`
	Message   string `json:"message"`
}

type ServicePoolResetStatus struct {
	CheckedAt             *time.Time `json:"checked_at,omitempty"`
	TotalAccounts         int        `json:"total_accounts"`
	FreshAccounts         int        `json:"fresh_accounts"`
	PendingAccounts       int        `json:"pending_accounts"`
	Phase                 string     `json:"phase"`
	LastResetAt           *time.Time `json:"last_reset_at,omitempty"`
	LastResetKind         string     `json:"last_reset_kind,omitempty"`
	AffectedSubscriptions int        `json:"affected_subscriptions"`
	NotificationState     string     `json:"notification_state,omitempty"`
}

func validateServicePoolResetConfig(next ServicePoolResetConfig, discord, current OnboardingDiscordConfig) error {
	if utf8.RuneCountInString(next.Message) > 2000 {
		return infraerrors.BadRequest("POOL_RESET_CONFIG_INVALID", "Discord 通知内容最多 2000 个字符")
	}
	if !next.Enabled {
		return nil
	}
	channel, err := strconv.ParseUint(next.ChannelID, 10, 64)
	if err != nil || channel == 0 || strings.TrimSpace(next.Message) == "" || discord.GuildID == "" || (discord.BotToken == "" && current.BotToken == "") {
		return infraerrors.BadRequest("POOL_RESET_CONFIG_INVALID", "启用自动重置前，请配置 Discord 机器人、服务器、通知频道和通知内容")
	}
	return nil
}

type servicePoolResetSample struct {
	At           time.Time              `json:"at"`
	Five         *OpenAIRateLimitWindow `json:"five,omitempty"`
	Week         *OpenAIRateLimitWindow `json:"week,omitempty"`
	CreditsKnown bool                   `json:"credits_known"`
	CreditCount  int                    `json:"credit_count"`
	Credits      map[string]string      `json:"credits"`
}

type servicePoolResetEvidence struct {
	Kind      string    `json:"kind"`
	Batch     string    `json:"batch"`
	Credit    string    `json:"credit,omitempty"`
	At        time.Time `json:"at"`
	Confirmed bool      `json:"confirmed"`
}

type servicePoolResetState struct {
	Cohort      string                              `json:"cohort"`
	Samples     map[string]servicePoolResetSample   `json:"samples"`
	Pending     map[string]servicePoolResetEvidence `json:"pending"`
	SeenCredits map[string]string                   `json:"seen_credits"`
	LastResetAt time.Time                           `json:"last_reset_at"`
	Status      ServicePoolResetStatus              `json:"status"`
}

type servicePoolResetDecision struct {
	Key, Kind       string
	Observed, Total int
}

func servicePoolResetHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func servicePoolResetSampleFromUsage(usage *OpenAIQuotaUsage, now time.Time) (servicePoolResetSample, bool) {
	if usage == nil || usage.FetchedAt <= 0 || now.Sub(time.Unix(usage.FetchedAt, 0)) > time.Minute || time.Unix(usage.FetchedAt, 0).After(now.Add(5*time.Second)) {
		return servicePoolResetSample{}, false
	}
	sample := servicePoolResetSample{At: time.Unix(usage.FetchedAt, 0), CreditsKnown: usage.ResetCreditDetailsComplete, Credits: make(map[string]string)}
	if usage.RateLimit != nil {
		for _, window := range []*OpenAIRateLimitWindow{usage.RateLimit.PrimaryWindow, usage.RateLimit.SecondaryWindow} {
			if window == nil || math.IsNaN(window.UsedPercent) || math.IsInf(window.UsedPercent, 0) || window.UsedPercent < 0 || window.UsedPercent > 100 {
				continue
			}
			switch window.LimitWindowSeconds {
			case 5 * 3600:
				sample.Five = window
			case 7 * 24 * 3600:
				sample.Week = window
			}
		}
	}
	if usage.RateLimitResetCredits != nil {
		sample.CreditCount = usage.RateLimitResetCredits.AvailableCount
	}
	for _, credit := range usage.autoResetCandidates {
		expires, err := time.Parse(time.RFC3339, credit.ExpiresAt)
		if err == nil && expires.After(now) && credit.ID != "" {
			sample.Credits[servicePoolResetHash(credit.ID)] = expires.UTC().Format(time.RFC3339)
		}
	}
	if sample.CreditCount != len(sample.Credits) {
		sample.CreditsKnown = false
	}
	return sample, sample.Five != nil || sample.Week != nil || sample.CreditsKnown
}

func servicePoolUnexpectedWindowReset(before, after servicePoolResetSample, now time.Time) bool {
	if before.Five == nil || before.Week == nil || after.Five == nil || after.Week == nil {
		return false
	}
	if before.Five.UsedPercent < 5 || before.Week.UsedPercent < 5 || after.Five.UsedPercent > 1 || after.Week.UsedPercent > 1 {
		return false
	}
	if before.Five.ResetAt <= now.Add(10*time.Minute).Unix() || before.Week.ResetAt <= now.Add(30*time.Minute).Unix() {
		return false
	}
	if !before.CreditsKnown || !after.CreditsKnown || after.CreditCount < before.CreditCount {
		return false
	}
	for credit := range before.Credits {
		if _, exists := after.Credits[credit]; !exists {
			return false
		}
	}
	return true
}

func evaluateServicePoolReset(state *servicePoolResetState, cohort []string, samples map[string]servicePoolResetSample, now time.Time) *servicePoolResetDecision {
	freshSamples := make(map[string]servicePoolResetSample, len(samples))
	members := make(map[string]struct{}, len(cohort))
	for _, identity := range cohort {
		members[identity] = struct{}{}
	}
	for identity, sample := range samples {
		_, member := members[identity]
		if member && now.Sub(sample.At) <= 3*time.Minute && !sample.At.After(now.Add(5*time.Second)) {
			freshSamples[identity] = sample
		}
	}
	samples = freshSamples
	if state.SeenCredits == nil {
		state.SeenCredits = make(map[string]string)
	}
	for credit, expiry := range state.SeenCredits {
		if expiresAt, err := time.Parse(time.RFC3339, expiry); err == nil && !expiresAt.After(now) {
			delete(state.SeenCredits, credit)
		}
	}
	sort.Strings(cohort)
	fingerprint := servicePoolResetHash(strings.Join(cohort, ":"))
	status := ServicePoolResetStatus{CheckedAt: &now, TotalAccounts: len(cohort), FreshAccounts: len(samples), Phase: "monitoring"}
	if state.Cohort != fingerprint || state.Samples == nil || state.Pending == nil {
		state.Cohort, state.Samples, state.Pending = fingerprint, samples, make(map[string]servicePoolResetEvidence)
		for identity, sample := range samples {
			for credit, expiry := range sample.Credits {
				state.SeenCredits[identity+":"+credit] = expiry
			}
		}
		status.Phase = "warming_up"
		state.Status = status
		return nil
	}
	if len(cohort) < 3 {
		status.Phase = "insufficient_accounts"
	}
	for _, identity := range cohort {
		after, fresh := samples[identity]
		before, known := state.Samples[identity]
		pending, hasPending := state.Pending[identity]
		if hasPending && (now.Sub(pending.At) > servicePoolResetEvidenceTTL || now.Before(pending.At)) {
			delete(state.Pending, identity)
			hasPending = false
		}
		if !fresh {
			continue
		}
		if known && !after.At.After(before.At) {
			continue
		}
		if hasPending && after.At.After(pending.At.Add(30*time.Second)) {
			if pending.Kind == "credits" {
				batch, exists := after.Credits[pending.Credit]
				pending.Confirmed = after.CreditsKnown && exists && batch == pending.Batch
			} else {
				pending.Confirmed = after.Five != nil && after.Week != nil && after.Five.UsedPercent <= 5 && after.Week.UsedPercent <= 5 && after.CreditsKnown
			}
			if pending.Confirmed {
				state.Pending[identity] = pending
			} else {
				delete(state.Pending, identity)
			}
		}
		if known && after.At.After(before.At) && after.At.Sub(before.At) <= servicePoolResetMaxSampleGap && now.Sub(after.At) <= 3*time.Minute {
			if before.CreditsKnown && after.CreditsKnown && after.CreditCount > before.CreditCount {
				credits := make([]string, 0, len(after.Credits))
				for credit := range after.Credits {
					credits = append(credits, credit)
				}
				sort.Slice(credits, func(first, second int) bool {
					return after.Credits[credits[first]]+credits[first] < after.Credits[credits[second]]+credits[second]
				})
				for _, credit := range credits {
					batch := after.Credits[credit]
					if _, seen := state.SeenCredits[identity+":"+credit]; !seen {
						state.Pending[identity] = servicePoolResetEvidence{Kind: "credits", Batch: batch, Credit: credit, At: after.At}
						break
					}
				}
			} else if servicePoolUnexpectedWindowReset(before, after, now) {
				state.Pending[identity] = servicePoolResetEvidence{Kind: "windows", Batch: "windows", At: after.At}
			}
		}
		for credit, expiry := range after.Credits {
			state.SeenCredits[identity+":"+credit] = expiry
		}
		state.Samples[identity] = after
	}
	counts := make(map[string]int)
	for identity, pending := range state.Pending {
		if pending.Confirmed {
			if current, exists := samples[identity]; exists && now.Sub(current.At) <= 3*time.Minute {
				counts[pending.Kind+":"+pending.Batch]++
			}
		}
	}
	status.PendingAccounts = len(state.Pending)
	if len(cohort) < 3 {
		status.Phase = "insufficient_accounts"
	} else if len(samples)*10 < len(cohort)*9 {
		status.Phase = "incomplete"
	} else if len(state.Pending) > 0 {
		status.Phase = "confirming"
	}
	state.Status = status
	if len(cohort) < 3 {
		return nil
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		count := counts[key]
		if count*10 >= len(cohort)*9 {
			if now.Sub(state.LastResetAt) < servicePoolResetEvidenceTTL {
				state.Pending = make(map[string]servicePoolResetEvidence)
				state.Status.PendingAccounts = 0
				return nil
			}
			kind := strings.SplitN(key, ":", 2)[0]
			eventKey := key
			if kind == "windows" {
				eventKey += ":" + now.UTC().Format(time.RFC3339Nano) + ":" + fingerprint
			}
			return &servicePoolResetDecision{Key: servicePoolResetHash(eventKey), Kind: kind, Observed: count, Total: len(cohort)}
		}
	}
	return nil
}
