package service

import (
	"context"
	"encoding/json"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrOnboardingTemplateRequired = infraerrors.BadRequest("ONBOARDING_TEMPLATE_REQUIRED", "管理员尚未指定有效的 GPT 模板账号，请联系管理员完成服务配置")

func validOnboardingTemplate(account *Account) bool {
	return account != nil && account.Platform == PlatformOpenAI && account.Type == AccountTypeOAuth && account.ParentAccountID == nil
}

func (s *OnboardingService) accountTemplate(ctx context.Context, id int64) (*Account, error) {
	if id <= 0 || s.admin == nil {
		return nil, ErrOnboardingTemplateRequired
	}
	account, err := s.admin.GetAccount(ctx, id)
	if err != nil || !validOnboardingTemplate(account) {
		return nil, ErrOnboardingTemplateRequired
	}
	return account, nil
}

func onboardingTemplateSettings(source map[string]any, keys []string) map[string]any {
	selected := make(map[string]any)
	for _, key := range keys {
		if value, exists := source[key]; exists {
			selected[key] = value
		}
	}
	payload, _ := json.Marshal(selected)
	var copied map[string]any
	_ = json.Unmarshal(payload, &copied)
	if copied == nil {
		copied = make(map[string]any)
	}
	return copied
}

func onboardingAccountFromTemplate(template *Account, credentials, extra map[string]any) *CreateAccountInput {
	rules := onboardingTemplateSettings(template.Credentials, []string{
		"model_mapping", "compact_model_mapping", "openai_capabilities",
		"custom_error_codes_enabled", "custom_error_codes", "temp_unschedulable_enabled", "temp_unschedulable_rules",
		"pool_mode", "pool_mode_retry_count", "pool_mode_retry_status_codes", "intercept_warmup_requests",
		"account_scheduling_threshold",
	})
	for key, value := range credentials {
		rules[key] = value
	}
	settings := onboardingTemplateSettings(template.Extra, []string{
		"openai_passthrough", "openai_oauth_passthrough", "openai_oauth_responses_websockets_v2_mode",
		"openai_oauth_responses_websockets_v2_enabled", "responses_websockets_v2_enabled", "openai_ws_enabled",
		"openai_ws_force_http", "openai_ws_allow_store_recovery", "openai_responses_flatten_namespaces",
		"openai_long_context_billing_enabled", "openai_compact_mode", "codex_cli_only", "codex_cli_only_allow_app_server",
		"codex_fingerprint_mode", "codex_image_generation_bridge", "codex_image_generation_explicit_tool_policy",
		"auto_pause_5h_threshold", "auto_pause_7d_threshold", "auto_pause_5h_disabled", "auto_pause_7d_disabled",
		"auto_reset_credit_enabled", "auto_reset_credit_5h_threshold", "auto_reset_credit_7d_threshold",
	})
	for key, value := range extra {
		settings[key] = value
	}
	rate := template.BillingRateMultiplier()
	return &CreateAccountInput{
		Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: rules, Extra: settings,
		ProxyID: template.ProxyID, Concurrency: template.Concurrency, Priority: template.Priority,
		RateMultiplier: &rate, LoadFactor: template.LoadFactor, GroupIDs: append([]int64{}, template.GroupIDs...),
		SkipDefaultGroupBind: true,
	}
}
