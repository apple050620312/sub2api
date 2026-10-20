//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOnboardingTemplateCopiesInvocationRulesWithoutIdentityOrRuntime(t *testing.T) {
	proxy, load := int64(9), 7
	rate := 0.25
	template := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ProxyID: &proxy, Concurrency: 11, Priority: 4, RateMultiplier: &rate, LoadFactor: &load, GroupIDs: []int64{5}, Credentials: map[string]any{
		"access_token": "template-secret", "refresh_token": "template-refresh", "email": "template@example.com", "chatgpt_account_id": "template-identity", "plan_type": "plus",
		"model_mapping": map[string]any{"model-a": "model-b"}, "account_scheduling_threshold": 85,
		"temp_unschedulable_rules": []any{map[string]any{"error_code": 429}},
	}, Extra: map[string]any{"codex_cli_only": true, "openai_compact_mode": "always", "auto_reset_credit_enabled": true, "codex_auto_reset_credit_state": map[string]any{"status": "success"}, "codex_5h_used_percent": 95, onboardingOwnerExtraKey: 99}}
	input := onboardingAccountFromTemplate(template, map[string]any{"access_token": "new-secret", "refresh_token": "new-refresh", "email": "owner@example.com", "plan_type": "team"}, map[string]any{onboardingOwnerExtraKey: 7})
	require.Equal(t, "new-secret", input.Credentials["access_token"])
	require.Equal(t, "new-refresh", input.Credentials["refresh_token"])
	require.Equal(t, "owner@example.com", input.Credentials["email"])
	require.Equal(t, "team", input.Credentials["plan_type"])
	require.NotContains(t, input.Credentials, "chatgpt_account_id")
	require.NotContains(t, input.Extra, "codex_auto_reset_credit_state")
	require.NotContains(t, input.Extra, "codex_5h_used_percent")
	require.Equal(t, 7, input.Extra[onboardingOwnerExtraKey])
	require.Equal(t, true, input.Extra["codex_cli_only"])
	require.Equal(t, "always", input.Extra["openai_compact_mode"])
	require.Equal(t, true, input.Extra["auto_reset_credit_enabled"])
	require.Equal(t, 11, input.Concurrency)
	require.Equal(t, 4, input.Priority)
	require.Equal(t, proxy, *input.ProxyID)
	require.Equal(t, rate, *input.RateMultiplier)
	require.Equal(t, load, *input.LoadFactor)
	require.Equal(t, []int64{5}, input.GroupIDs)
	require.Nil(t, input.ExpiresAt)
	require.True(t, input.SkipDefaultGroupBind)
	input.Credentials["model_mapping"].(map[string]any)["model-a"] = "changed"
	input.GroupIDs[0] = 99
	require.Equal(t, "model-b", template.Credentials["model_mapping"].(map[string]any)["model-a"])
	require.Equal(t, []int64{5}, template.GroupIDs)
}

func TestOnboardingTemplateRejectsMissingWrongPlatformAndShadow(t *testing.T) {
	parent := int64(1)
	for _, account := range []Account{
		{ID: 42, Platform: PlatformAnthropic, Type: AccountTypeOAuth},
		{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent},
	} {
		svc := &ServiceCenterService{admin: &onboardingTestAdmin{accounts: []Account{account}}}
		_, err := svc.accountTemplate(context.Background(), 42)
		require.ErrorIs(t, err, ErrOnboardingTemplateRequired)
	}
	svc := &ServiceCenterService{admin: &onboardingTestAdmin{}}
	_, err := svc.accountTemplate(context.Background(), 0)
	require.ErrorIs(t, err, ErrOnboardingTemplateRequired)
	_, err = svc.accountTemplate(context.Background(), 42)
	require.ErrorIs(t, err, ErrOnboardingTemplateRequired)
}
