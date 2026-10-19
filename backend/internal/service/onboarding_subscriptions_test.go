//go:build unit

package service

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOnboardingPlanEquivalents(t *testing.T) {
	for _, test := range []struct {
		plan      string
		canonical string
		units     int
	}{
		{"plus", "plus", 1},
		{"ChatGPT Plus", "plus", 1},
		{"team", "business_standard", 1},
		{"self_serve_business_usage_based", "business_standard", 1},
		{"Business Standard", "business_standard", 1},
		{"self_serve_business_prolite", "business_premium", 5},
		{"business_premium", "business_premium", 5},
		{"prolite", "pro_5x", 5},
		{"Pro 5x", "pro_5x", 5},
		{"chatgptpro", "pro", 20},
		{"promax", "pro_max", 50},
		{"business", "business", 0},
		{"free", "free", 0},
	} {
		t.Run(test.plan, func(t *testing.T) {
			require.Equal(t, test.canonical, normalizeOnboardingPlan(test.plan))
			require.Equal(t, test.units, onboardingPlanUnits(test.plan))
		})
	}
}

func TestOnboardingSelectsOneSubscriptionByTotalAccountCount(t *testing.T) {
	rules := make(map[string]OnboardingSubscriptionRule)
	for tier := 1; tier <= 5; tier++ {
		rules[strconv.Itoa(tier)] = OnboardingSubscriptionRule{GroupID: int64(100 + tier), ValidityDays: 30}
	}
	for _, test := range []struct {
		plans []string
		group int64
	}{
		{nil, 0},
		{[]string{"plus"}, 101},
		{[]string{"plus", "team"}, 102},
		{[]string{"plus", "plus", "plus"}, 103},
		{[]string{"plus", "plus", "plus", "plus"}, 104},
		{[]string{"plus", "plus", "plus", "plus", "plus"}, 105},
		{[]string{"prolite"}, 105},
		{[]string{"self_serve_business_prolite"}, 105},
		{[]string{"prolite", "plus"}, 105},
		{[]string{"chatgptpro"}, 105},
	} {
		units := 0
		for _, plan := range test.plans {
			units += onboardingPlanUnits(plan)
		}
		selected := selectOnboardingSubscription(units, rules)
		if test.group == 0 {
			require.Empty(t, selected)
		} else {
			require.Equal(t, map[int64]OnboardingSubscriptionRule{test.group: {GroupID: test.group, ValidityDays: 30}}, selected)
		}
	}
	delete(rules, "2")
	require.Empty(t, selectOnboardingSubscription(2, rules), "an unconfigured tier must not silently grant another subscription")
}

func TestOnboardingTierChangesSuspendOnlyPreviousAutomaticTier(t *testing.T) {
	now := time.Now()
	repo := &onboardingTestSubscriptions{subs: []UserSubscription{
		{ID: 1, GroupID: 101, Status: SubscriptionStatusActive, StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), WeeklyUsageUSD: 7, Notes: onboardingManagedNotes + ":account"},
		{ID: 2, GroupID: 102, Status: SubscriptionStatusSuspended, StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), Notes: onboardingManagedNotes + ":account:account_paused"},
		{ID: 3, GroupID: 999, Status: SubscriptionStatusActive, StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), Notes: "purchased"},
	}}
	svc := &OnboardingService{subscriptions: &SubscriptionService{userSubRepo: repo}}
	rules := map[string]OnboardingSubscriptionRule{"1": {GroupID: 101}, "2": {GroupID: 102}}
	for _, count := range []int{2, 1} {
		groups, err := svc.syncOnboardingSubscriptions(context.Background(), 7, selectOnboardingSubscription(count, rules), repo.subs, now)
		require.NoError(t, err)
		require.Equal(t, map[int64]struct{}{int64(100 + count): {}}, groups)
		require.Equal(t, SubscriptionStatusActive, repo.subs[count-1].Status)
		require.Equal(t, SubscriptionStatusSuspended, repo.subs[2-count].Status)
		require.Equal(t, SubscriptionStatusActive, repo.subs[2].Status)
	}
	require.Equal(t, 7.0, repo.subs[0].WeeklyUsageUSD)
	require.Equal(t, now.Add(time.Hour), repo.subs[0].ExpiresAt)
}

func TestOnboardingLegacySubscriptionRulesMigration(t *testing.T) {
	for _, test := range []struct {
		name   string
		raw    string
		groups map[string]int64
		review bool
	}{
		{"new tiers", `{"subscription_rules":{"2":{"group_id":12,"validity_days":30}}}`, map[string]int64{"2": 12}, false},
		{"unambiguous legacy", `{"plan_units":{"plus":1},"subscription_rules":{"plus":{"group_id":11,"validity_days":30,"units":1},"pro":{"group_id":15,"validity_days":30,"units":5}}}`, map[string]int64{"1": 11, "5": 15}, false},
		{"conflicting legacy", `{"subscription_rules":{"plus":{"group_id":11,"units":1},"business_standard":{"group_id":12,"units":1}}}`, map[string]int64{}, true},
		{"same group legacy", `{"subscription_rules":{"plus":{"group_id":11,"units":1},"business_standard":{"group_id":11,"units":1}}}`, map[string]int64{"1": 11}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var config OnboardingConfig
			require.NoError(t, json.Unmarshal([]byte(test.raw), &config))
			require.NoError(t, normalizeOnboardingSubscriptionRules([]byte(test.raw), &config))
			groups := make(map[string]int64)
			for tier, rule := range config.SubscriptionRules {
				groups[tier] = rule.GroupID
			}
			require.Equal(t, test.groups, groups)
			require.Equal(t, test.review, config.SubscriptionRulesNeedReview)
			encoded, err := json.Marshal(config)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "plan_units")
			require.NotContains(t, string(encoded), `"units"`)
		})
	}
}
