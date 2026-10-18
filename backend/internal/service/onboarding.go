package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

const (
	onboardingOwnerExtraKey          = "onboarding_owner_user_id"
	onboardingPublicFallbackExtraKey = "onboarding_public_fallback"
	onboardingManagedNotes           = "onboarding-managed"
	onboardingStateTTL               = 10 * time.Minute
)

var (
	ErrOnboardingDisabled       = infraerrors.Forbidden("ONBOARDING_DISABLED", "新手引导已关闭")
	ErrDiscordNotConfigured     = infraerrors.BadRequest("DISCORD_NOT_CONFIGURED", "Discord 引导尚未配置完成")
	ErrDiscordBindingRequired   = infraerrors.Forbidden("DISCORD_BINDING_REQUIRED", "请先绑定 Discord")
	ErrOnboardingStateInvalid   = infraerrors.BadRequest("ONBOARDING_STATE_INVALID", "引导状态无效或已过期")
	ErrUnsupportedOpenAIPlan    = infraerrors.BadRequest("OPENAI_PLAN_UNSUPPORTED", "当前 OpenAI 套餐不支持自行添加")
	ErrOnboardingAccountMissing = infraerrors.NotFound("ONBOARDING_ACCOUNT_NOT_FOUND", "账号不存在")
)

// OnboardingDiscordConfig is administrator-controlled Discord integration.
// ClientSecret and BotToken are write-only fields and are never returned.
type OnboardingDiscordConfig struct {
	ClientID        string `json:"client_id"`
	ClientSecret    string `json:"-"`
	BotToken        string `json:"-"`
	ClientSecretSet bool   `json:"client_secret_configured"`
	BotTokenSet     bool   `json:"bot_token_configured"`
	GuildID         string `json:"guild_id"`
	RoleID          string `json:"role_id"`
	RedirectURI     string `json:"redirect_uri"`
}

type OnboardingSubscriptionRule struct {
	GroupID      int64 `json:"group_id"`
	ValidityDays int   `json:"validity_days"`
	Units        int   `json:"units"`
}

// OnboardingConfig contains all public onboarding configuration.
type OnboardingConfig struct {
	Enabled               bool                                  `json:"enabled"`
	Discord               OnboardingDiscordConfig               `json:"discord"`
	PlanUnits             map[string]int                        `json:"plan_units"`
	SubscriptionRules     map[string]OnboardingSubscriptionRule `json:"subscription_rules"`
	OpenAIRedirectURI     string                                `json:"openai_redirect_uri"`
	CCSwitchDownloadURL   string                                `json:"ccswitch_download_url"`
	PublicFallbackDefault bool                                  `json:"public_fallback_default"`
}

type onboardingStoredConfig struct {
	OnboardingConfig
	DiscordClientSecretEncrypted string `json:"discord_client_secret_encrypted,omitempty"`
	DiscordBotTokenEncrypted     string `json:"discord_bot_token_encrypted,omitempty"`
}

type OnboardingDiscordBinding struct {
	UserID          int64      `json:"user_id"`
	DiscordUserID   string     `json:"discord_user_id"`
	DiscordUsername string     `json:"discord_username"`
	GuildID         string     `json:"guild_id"`
	RoleID          string     `json:"role_id"`
	Status          string     `json:"status"`
	LastCheckedAt   *time.Time `json:"last_checked_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type OnboardingAccountSummary struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	PlanType    string `json:"plan_type"`
	Status      string `json:"status"`
	Public      bool   `json:"public_fallback"`
	OwnerSource string `json:"owner_source,omitempty"`
}

type OnboardingUserOverview struct {
	Discord         *OnboardingDiscordBinding  `json:"discord,omitempty"`
	DiscordRequired bool                       `json:"discord_required"`
	Accounts        []OnboardingAccountSummary `json:"accounts"`
	AccountUnits    int                        `json:"account_units"`
	ApiKeyCount     int64                      `json:"api_key_count"`
	ReadyForAccount bool                       `json:"ready_for_account"`
	ReadyForKey     bool                       `json:"ready_for_key"`
	ReadyForCCS     bool                       `json:"ready_for_ccs"`
	Config          OnboardingConfig           `json:"config"`
}

type OnboardingAdminUser struct {
	UserID       int64                      `json:"user_id"`
	Email        string                     `json:"email"`
	Username     string                     `json:"username"`
	Discord      *OnboardingDiscordBinding  `json:"discord,omitempty"`
	Accounts     []OnboardingAccountSummary `json:"accounts"`
	AccountUnits int                        `json:"account_units"`
}

type OnboardingAdminOverview struct {
	Config             OnboardingConfig           `json:"config"`
	Users              []OnboardingAdminUser      `json:"users"`
	UnassignedAccounts []OnboardingAccountSummary `json:"unassigned_accounts"`
}

type OnboardingOAuthStart struct {
	AuthURL         string `json:"auth_url"`
	SessionID       string `json:"session_id"`
	OnboardingState string `json:"onboarding_state"`
	RedirectURI     string `json:"redirect_uri"`
}

type OnboardingService struct {
	db            *dbent.Client
	admin         AdminService
	oauth         *OpenAIOAuthService
	subscriptions *SubscriptionService
	cfg           *config.Config
	encryptor     SecretEncryptor
	httpClient    *http.Client
}

func NewOnboardingService(db *dbent.Client, admin AdminService, oauth *OpenAIOAuthService, subscriptions *SubscriptionService, cfg *config.Config, encryptor SecretEncryptor) *OnboardingService {
	return &OnboardingService{db: db, admin: admin, oauth: oauth, subscriptions: subscriptions, cfg: cfg, encryptor: encryptor, httpClient: &http.Client{Timeout: 12 * time.Second}}
}

func defaultOnboardingConfig() OnboardingConfig {
	return OnboardingConfig{
		Enabled:           true,
		PlanUnits:         map[string]int{"plus": 1, "pro": 5, "business_premium": 5, "business_standard": 1},
		SubscriptionRules: map[string]OnboardingSubscriptionRule{},
	}
}

func (s *OnboardingService) loadConfig(ctx context.Context) (*onboardingStoredConfig, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("onboarding database is unavailable")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT config FROM onboarding_config WHERE id = 1`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, sql.ErrNoRows
	}
	var raw []byte
	if err := rows.Scan(&raw); err != nil {
		return nil, err
	}
	stored := onboardingStoredConfig{OnboardingConfig: defaultOnboardingConfig()}
	if len(raw) != 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &stored); err != nil {
			return nil, fmt.Errorf("decode onboarding config: %w", err)
		}
	}
	if stored.PlanUnits == nil {
		stored.PlanUnits = defaultOnboardingConfig().PlanUnits
	}
	if stored.SubscriptionRules == nil {
		stored.SubscriptionRules = map[string]OnboardingSubscriptionRule{}
	}
	if stored.DiscordClientSecretEncrypted != "" && s.encryptor != nil {
		stored.Discord.ClientSecret, _ = s.encryptor.Decrypt(stored.DiscordClientSecretEncrypted)
	}
	if stored.DiscordBotTokenEncrypted != "" && s.encryptor != nil {
		stored.Discord.BotToken, _ = s.encryptor.Decrypt(stored.DiscordBotTokenEncrypted)
	}
	stored.Discord.ClientSecretSet = stored.Discord.ClientSecret != ""
	stored.Discord.BotTokenSet = stored.Discord.BotToken != ""
	return &stored, nil
}

func publicOnboardingConfig(stored *onboardingStoredConfig) OnboardingConfig {
	if stored == nil {
		return defaultOnboardingConfig()
	}
	public := stored.OnboardingConfig
	public.Discord.ClientSecret = ""
	public.Discord.BotToken = ""
	public.Discord.ClientSecretSet = stored.Discord.ClientSecret != ""
	public.Discord.BotTokenSet = stored.Discord.BotToken != ""
	return public
}

func (s *OnboardingService) GetConfig(ctx context.Context) (OnboardingConfig, error) {
	stored, err := s.loadConfig(ctx)
	if err != nil {
		return OnboardingConfig{}, err
	}
	return publicOnboardingConfig(stored), nil
}

func (s *OnboardingService) UpdateConfig(ctx context.Context, next OnboardingConfig) (OnboardingConfig, error) {
	current, err := s.loadConfig(ctx)
	if err != nil {
		return OnboardingConfig{}, err
	}
	if next.PlanUnits == nil {
		next.PlanUnits = current.PlanUnits
	}
	if next.SubscriptionRules == nil {
		next.SubscriptionRules = current.SubscriptionRules
	}
	if strings.TrimSpace(next.Discord.ClientSecret) == "" {
		next.Discord.ClientSecret = current.Discord.ClientSecret
	}
	if strings.TrimSpace(next.Discord.BotToken) == "" {
		next.Discord.BotToken = current.Discord.BotToken
	}
	stored := onboardingStoredConfig{OnboardingConfig: next}
	if next.Discord.ClientSecret != "" {
		if s.encryptor == nil {
			return OnboardingConfig{}, fmt.Errorf("secret encryption is unavailable")
		}
		stored.DiscordClientSecretEncrypted, err = s.encryptor.Encrypt(next.Discord.ClientSecret)
		if err != nil {
			return OnboardingConfig{}, err
		}
	}
	if next.Discord.BotToken != "" {
		if s.encryptor == nil {
			return OnboardingConfig{}, fmt.Errorf("secret encryption is unavailable")
		}
		stored.DiscordBotTokenEncrypted, err = s.encryptor.Encrypt(next.Discord.BotToken)
		if err != nil {
			return OnboardingConfig{}, err
		}
	}
	payload, err := json.Marshal(stored)
	if err != nil {
		return OnboardingConfig{}, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE onboarding_config SET config = $1::jsonb, updated_at = NOW() WHERE id = 1`, payload); err != nil {
		return OnboardingConfig{}, err
	}
	return publicOnboardingConfig(&stored), nil
}

func (s *OnboardingService) getBinding(ctx context.Context, userID int64) (*OnboardingDiscordBinding, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id, discord_user_id, discord_username, guild_id, role_id, status, last_checked_at, created_at, updated_at FROM onboarding_discord_bindings WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var binding OnboardingDiscordBinding
	if err := rows.Scan(&binding.UserID, &binding.DiscordUserID, &binding.DiscordUsername, &binding.GuildID, &binding.RoleID, &binding.Status, &binding.LastCheckedAt, &binding.CreatedAt, &binding.UpdatedAt); err != nil {
		return nil, err
	}
	return &binding, nil
}

func (s *OnboardingService) listBindings(ctx context.Context) ([]OnboardingDiscordBinding, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id, discord_user_id, discord_username, guild_id, role_id, status, last_checked_at, created_at, updated_at FROM onboarding_discord_bindings ORDER BY user_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	bindings := make([]OnboardingDiscordBinding, 0)
	for rows.Next() {
		var binding OnboardingDiscordBinding
		if err := rows.Scan(&binding.UserID, &binding.DiscordUserID, &binding.DiscordUsername, &binding.GuildID, &binding.RoleID, &binding.Status, &binding.LastCheckedAt, &binding.CreatedAt, &binding.UpdatedAt); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

func (s *OnboardingService) requireEnabled(ctx context.Context) (*onboardingStoredConfig, error) {
	stored, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !stored.Enabled {
		return nil, ErrOnboardingDisabled
	}
	return stored, nil
}

func (s *OnboardingService) requireDiscord(ctx context.Context, userID int64) (*onboardingStoredConfig, error) {
	stored, err := s.requireEnabled(ctx)
	if err != nil {
		return nil, err
	}
	binding, err := s.getBinding(ctx, userID)
	if err != nil {
		return nil, err
	}
	if binding == nil || binding.Status != "active" {
		return nil, ErrDiscordBindingRequired
	}
	return stored, nil
}

func accountOwnerID(account Account) int64 {
	value := account.Extra[onboardingOwnerExtraKey]
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	case string:
		id, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return id
	default:
		return 0
	}
}

func accountPublicFallback(account Account) bool {
	flag, _ := account.Extra[onboardingPublicFallbackExtraKey].(bool)
	return flag
}

func onboardingAccountOwnerUserID(account *Account) int64 {
	if account == nil {
		return 0
	}
	return accountOwnerID(*account)
}

func onboardingAccountIsPublic(account *Account) bool {
	if account == nil {
		return false
	}
	return onboardingAccountOwnerUserID(account) == 0 || accountPublicFallback(*account)
}

func onboardingAccountBelongsToUser(account *Account, userID int64) bool {
	return account != nil && userID > 0 && onboardingAccountOwnerUserID(account) == userID
}

func onboardingAccountAllowedForUser(account *Account, userID int64) bool {
	if userID <= 0 {
		return true
	}
	return onboardingAccountBelongsToUser(account, userID) || onboardingAccountIsPublic(account)
}

func normalizeOnboardingPlan(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(strings.ReplaceAll(value, "-", "_"), " ", "_")
	switch value {
	case "chatgpt_plus", "plus", "chatgptplus":
		return "plus"
	case "chatgpt_pro", "pro", "chatgptpro":
		return "pro"
	case "business_premium", "businesspremium", "team":
		return "business_premium"
	case "business_standard", "businessstandard":
		return "business_standard"
	default:
		return value
	}
}

func onboardingPlanUnits(plan string, configured map[string]int) int {
	plan = normalizeOnboardingPlan(plan)
	if units := configured[plan]; units > 0 {
		return units
	}
	switch plan {
	case "plus", "business_standard":
		return 1
	case "pro", "business_premium":
		return 5
	default:
		return 0
	}
}

func (s *OnboardingService) listUserAccounts(ctx context.Context, userID int64) ([]Account, int, error) {
	accounts, _, err := s.admin.ListAccounts(ctx, 1, 1000, PlatformOpenAI, AccountTypeOAuth, "", "", 0, "", "created_at", "asc")
	if err != nil {
		return nil, 0, err
	}
	stored, err := s.loadConfig(ctx)
	if err != nil {
		return nil, 0, err
	}
	owned := make([]Account, 0)
	units := 0
	for _, account := range accounts {
		if accountOwnerID(account) != userID {
			continue
		}
		owned = append(owned, account)
		if account.Status == StatusActive {
			units += onboardingPlanUnits(account.GetCredential("plan_type"), stored.PlanUnits)
		}
	}
	return owned, units, nil
}

func (s *OnboardingService) GetUserOverview(ctx context.Context, userID int64) (*OnboardingUserOverview, error) {
	stored, err := s.requireEnabled(ctx)
	if err != nil {
		return nil, err
	}
	binding, err := s.getBinding(ctx, userID)
	if err != nil {
		return nil, err
	}
	accounts, units, err := s.listUserAccounts(ctx, userID)
	if err != nil {
		return nil, err
	}
	_, keyCount, keyErr := s.admin.GetUserAPIKeys(ctx, userID, 1, 1, "created_at", "desc")
	if keyErr != nil {
		keyCount = 0
	}
	summaries := make([]OnboardingAccountSummary, 0, len(accounts))
	for _, account := range accounts {
		summaries = append(summaries, OnboardingAccountSummary{ID: account.ID, Name: account.Name, PlanType: normalizeOnboardingPlan(account.GetCredential("plan_type")), Status: account.Status, Public: accountPublicFallback(account), OwnerSource: fmt.Sprint(account.Extra["onboarding_owner_source"])})
	}
	return &OnboardingUserOverview{
		Discord: binding, DiscordRequired: true, Accounts: summaries, AccountUnits: units,
		ApiKeyCount: keyCount, ReadyForAccount: binding != nil && binding.Status == "active",
		ReadyForKey: binding != nil && binding.Status == "active" && len(summaries) > 0,
		ReadyForCCS: keyCount > 0, Config: publicOnboardingConfig(stored),
	}, nil
}

func (s *OnboardingService) signState(userID int64, kind string) (string, error) {
	if s.cfg == nil || strings.TrimSpace(s.cfg.JWT.Secret) == "" {
		return "", fmt.Errorf("JWT secret is not configured")
	}
	payload := fmt.Sprintf("%s|%d|%d", kind, userID, time.Now().Unix())
	mac := hmac.New(sha256.New, []byte(s.cfg.JWT.Secret))
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *OnboardingService) verifyState(state, kind string, userID int64) error {
	parts := strings.Split(state, ".")
	if len(parts) != 2 || s.cfg == nil || strings.TrimSpace(s.cfg.JWT.Secret) == "" {
		return ErrOnboardingStateInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ErrOnboardingStateInvalid
	}
	mac := hmac.New(sha256.New, []byte(s.cfg.JWT.Secret))
	_, _ = mac.Write(payload)
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(provided, mac.Sum(nil)) {
		return ErrOnboardingStateInvalid
	}
	fields := strings.Split(string(payload), "|")
	if len(fields) != 3 || fields[0] != kind {
		return ErrOnboardingStateInvalid
	}
	parsedUser, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || parsedUser != userID {
		return ErrOnboardingStateInvalid
	}
	created, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || time.Since(time.Unix(created, 0)) > onboardingStateTTL || time.Since(time.Unix(created, 0)) < -time.Minute {
		return ErrOnboardingStateInvalid
	}
	return nil
}

func (s *OnboardingService) StartDiscord(ctx context.Context, userID int64, redirectURI string) (string, error) {
	stored, err := s.requireEnabled(ctx)
	if err != nil {
		return "", err
	}
	if stored.Discord.ClientID == "" || stored.Discord.GuildID == "" || stored.Discord.ClientSecret == "" || stored.Discord.BotToken == "" {
		return "", ErrDiscordNotConfigured
	}
	if strings.TrimSpace(stored.Discord.RedirectURI) != "" {
		redirectURI = stored.Discord.RedirectURI
	}
	if strings.TrimSpace(redirectURI) == "" {
		return "", infraerrors.BadRequest("DISCORD_REDIRECT_URI_REQUIRED", "Discord redirect URI is required")
	}
	state, err := s.signState(userID, "discord")
	if err != nil {
		return "", err
	}
	query := url.Values{"client_id": {stored.Discord.ClientID}, "response_type": {"code"}, "redirect_uri": {redirectURI}, "scope": {"identify guilds.join"}, "state": {state}}
	return "https://discord.com/oauth2/authorize?" + query.Encode(), nil
}

func decodeJSONResponse(resp *http.Response, target any) error {
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upstream returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if target == nil || len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, target)
}

func (s *OnboardingService) CompleteDiscord(ctx context.Context, userID int64, code, state, redirectURI string) (*OnboardingDiscordBinding, error) {
	stored, err := s.requireEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.verifyState(state, "discord", userID); err != nil {
		return nil, err
	}
	if stored.Discord.RedirectURI != "" {
		redirectURI = stored.Discord.RedirectURI
	} else if redirectURI == "" {
		redirectURI = stored.Discord.RedirectURI
	}
	form := url.Values{"client_id": {stored.Discord.ClientID}, "client_secret": {stored.Discord.ClientSecret}, "grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://discord.com/api/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := decodeJSONResponse(resp, &token); err != nil {
		return nil, err
	}
	if token.AccessToken == "" {
		return nil, fmt.Errorf("discord returned no access token")
	}
	userReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://discord.com/api/users/@me", nil)
	userReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
	userResp, err := s.httpClient.Do(userReq)
	if err != nil {
		return nil, err
	}
	var discordUser struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	if err := decodeJSONResponse(userResp, &discordUser); err != nil {
		return nil, err
	}
	if discordUser.ID == "" {
		return nil, fmt.Errorf("discord returned no user id")
	}
	joinURL := "https://discord.com/api/guilds/" + url.PathEscape(stored.Discord.GuildID) + "/members/" + url.PathEscape(discordUser.ID)
	joinBody, _ := json.Marshal(map[string]string{"access_token": token.AccessToken})
	joinReq, _ := http.NewRequestWithContext(ctx, http.MethodPut, joinURL, strings.NewReader(string(joinBody)))
	joinReq.Header.Set("Authorization", "Bot "+stored.Discord.BotToken)
	joinReq.Header.Set("Content-Type", "application/json")
	joinResp, err := s.httpClient.Do(joinReq)
	if err != nil {
		return nil, err
	}
	if err := decodeJSONResponse(joinResp, nil); err != nil {
		return nil, fmt.Errorf("join Discord server: %w", err)
	}
	if stored.Discord.RoleID != "" {
		roleReq, _ := http.NewRequestWithContext(ctx, http.MethodPut, joinURL+"/roles/"+url.PathEscape(stored.Discord.RoleID), nil)
		roleReq.Header.Set("Authorization", "Bot "+stored.Discord.BotToken)
		roleResp, err := s.httpClient.Do(roleReq)
		if err != nil {
			return nil, err
		}
		if err := decodeJSONResponse(roleResp, nil); err != nil {
			return nil, fmt.Errorf("grant Discord role: %w", err)
		}
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO onboarding_discord_bindings (user_id, discord_user_id, discord_username, guild_id, role_id, status, last_checked_at, updated_at) VALUES ($1,$2,$3,$4,$5,'active',NOW(),NOW()) ON CONFLICT (user_id) DO UPDATE SET discord_user_id=EXCLUDED.discord_user_id, discord_username=EXCLUDED.discord_username, guild_id=EXCLUDED.guild_id, role_id=EXCLUDED.role_id, status='active', last_checked_at=NOW(), updated_at=NOW()`, userID, discordUser.ID, discordUser.Username, stored.Discord.GuildID, stored.Discord.RoleID)
	if err != nil {
		return nil, err
	}
	return s.getBinding(ctx, userID)
}

func (s *OnboardingService) StartOpenAI(ctx context.Context, userID int64, _ string) (*OnboardingOAuthStart, error) {
	stored, err := s.requireDiscord(ctx, userID)
	if err != nil {
		return nil, err
	}
	redirectURI := strings.TrimSpace(stored.OpenAIRedirectURI)
	if redirectURI == "" {
		redirectURI = openai.DefaultRedirectURI
	}
	result, err := s.oauth.GenerateAuthURL(ctx, nil, redirectURI, PlatformOpenAI)
	if err != nil {
		return nil, err
	}
	state, err := s.signState(userID, "openai")
	if err != nil {
		return nil, err
	}
	return &OnboardingOAuthStart{AuthURL: result.AuthURL, SessionID: result.SessionID, OnboardingState: state, RedirectURI: redirectURI}, nil
}

func (s *OnboardingService) CompleteOpenAI(ctx context.Context, userID int64, sessionID, code, oauthState, onboardingState string, _ string) (*Account, error) {
	stored, err := s.requireDiscord(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := s.verifyState(onboardingState, "openai", userID); err != nil {
		return nil, err
	}
	redirectURI := strings.TrimSpace(stored.OpenAIRedirectURI)
	if redirectURI == "" {
		redirectURI = openai.DefaultRedirectURI
	}
	tokenInfo, err := s.oauth.ExchangeCode(ctx, &OpenAIExchangeCodeInput{SessionID: sessionID, Code: code, State: oauthState, RedirectURI: redirectURI})
	if err != nil {
		return nil, err
	}
	plan := normalizeOnboardingPlan(tokenInfo.PlanType)
	if onboardingPlanUnits(plan, stored.PlanUnits) <= 0 || plan == "free" || plan == "go" || plan == "k12" || plan == "" {
		return nil, ErrUnsupportedOpenAIPlan
	}
	name := "OpenAI " + plan + " 账号"
	extra := map[string]any{onboardingOwnerExtraKey: userID, onboardingPublicFallbackExtraKey: stored.PublicFallbackDefault, "onboarding_owner_source": "user_oauth", "onboarding_plan_type": plan}
	account, err := s.admin.CreateAccount(ctx, &CreateAccountInput{Name: name, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: s.oauth.BuildAccountCredentials(tokenInfo), Extra: extra, Concurrency: 3, Priority: 50, SkipDefaultGroupBind: true})
	if err != nil {
		return nil, err
	}
	if err := s.reconcileSubscriptions(ctx, userID); err != nil {
		return account, err
	}
	return s.admin.GetAccount(ctx, account.ID)
}

func (s *OnboardingService) reconcileSubscriptions(ctx context.Context, userID int64) error {
	if s.subscriptions == nil {
		return fmt.Errorf("订阅服务不可用")
	}
	stored, err := s.loadConfig(ctx)
	if err != nil {
		return err
	}
	accounts, units, err := s.listUserAccounts(ctx, userID)
	if err != nil {
		return err
	}
	selected := make(map[string]OnboardingSubscriptionRule)
	remaining := units
	for _, key := range []string{"pro", "business_premium", "plus", "business_standard"} {
		rule, ok := stored.SubscriptionRules[key]
		if !ok || rule.GroupID <= 0 {
			continue
		}
		cost := rule.Units
		if cost <= 0 {
			cost = onboardingPlanUnits(key, stored.PlanUnits)
		}
		if cost <= 0 || remaining < cost {
			continue
		}
		selected[key] = rule
		remaining -= cost
	}
	activeGroups := make(map[int64]struct{}, len(selected))
	for key, rule := range selected {
		if rule.ValidityDays <= 0 {
			rule.ValidityDays = 30
		}
		if _, _, err := s.subscriptions.AssignOrExtendSubscription(ctx, &AssignSubscriptionInput{UserID: userID, GroupID: rule.GroupID, ValidityDays: rule.ValidityDays, Notes: onboardingManagedNotes + ":" + key}); err != nil {
			return err
		}
		activeGroups[rule.GroupID] = struct{}{}
	}
	if subs, listErr := s.subscriptions.ListUserSubscriptions(ctx, userID); listErr == nil {
		for _, sub := range subs {
			if strings.HasPrefix(sub.Notes, onboardingManagedNotes+":") && sub.Status == SubscriptionStatusActive {
				if _, keep := activeGroups[sub.GroupID]; !keep {
					if err := s.subscriptions.RevokeSubscription(ctx, sub.ID); err != nil {
						return err
					}
				}
			}
		}
	}
	groupIDs := make([]int64, 0, len(activeGroups))
	for id := range activeGroups {
		groupIDs = append(groupIDs, id)
	}
	for _, account := range accounts {
		if account.Status != StatusActive || len(groupIDs) == 0 {
			continue
		}
		ids := append([]int64(nil), groupIDs...)
		if _, err := s.admin.UpdateAccount(ctx, account.ID, &UpdateAccountInput{GroupIDs: &ids, SkipMixedChannelCheck: true}); err != nil {
			return err
		}
	}
	return nil
}

func (s *OnboardingService) BindExistingAccount(ctx context.Context, userID, accountID int64, publicFallback bool) error {
	if _, err := s.requireDiscord(ctx, userID); err != nil {
		return err
	}
	account, err := s.admin.GetAccount(ctx, accountID)
	if err != nil || account == nil {
		return ErrOnboardingAccountMissing
	}
	if account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth {
		return infraerrors.BadRequest("ONBOARDING_ACCOUNT_INVALID", "only OpenAI OAuth accounts can be assigned")
	}
	if owner := accountOwnerID(*account); owner != 0 && owner != userID {
		return infraerrors.Conflict("ONBOARDING_ACCOUNT_ASSIGNED", "account is already assigned to another user")
	}
	if err := s.admin.UpdateAccountExtra(ctx, accountID, map[string]any{onboardingOwnerExtraKey: userID, onboardingPublicFallbackExtraKey: publicFallback, "onboarding_owner_source": "admin_assignment"}); err != nil {
		return err
	}
	return s.reconcileSubscriptions(ctx, userID)
}

func (s *OnboardingService) UnbindAccount(ctx context.Context, accountID int64) error {
	account, err := s.admin.GetAccount(ctx, accountID)
	if err != nil || account == nil {
		return ErrOnboardingAccountMissing
	}
	owner := accountOwnerID(*account)
	if err := s.admin.UpdateAccountExtra(ctx, accountID, map[string]any{onboardingOwnerExtraKey: nil, onboardingPublicFallbackExtraKey: false, "onboarding_owner_source": nil}); err != nil {
		return err
	}
	if owner > 0 {
		return s.reconcileSubscriptions(ctx, owner)
	}
	return nil
}

func (s *OnboardingService) AdminOverview(ctx context.Context) (*OnboardingAdminOverview, error) {
	stored, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	users, _, err := s.admin.ListUsers(ctx, 1, 1000, UserListFilters{}, "created_at", "asc")
	if err != nil {
		return nil, err
	}
	bindings, err := s.listBindings(ctx)
	if err != nil {
		return nil, err
	}
	bindingMap := make(map[int64]*OnboardingDiscordBinding, len(bindings))
	for i := range bindings {
		bindingMap[bindings[i].UserID] = &bindings[i]
	}
	overview := &OnboardingAdminOverview{Config: publicOnboardingConfig(stored), Users: make([]OnboardingAdminUser, 0, len(users)), UnassignedAccounts: make([]OnboardingAccountSummary, 0)}
	for _, user := range users {
		accounts, units, accountErr := s.listUserAccounts(ctx, user.ID)
		if accountErr != nil {
			return nil, accountErr
		}
		summaries := make([]OnboardingAccountSummary, 0, len(accounts))
		for _, account := range accounts {
			summaries = append(summaries, OnboardingAccountSummary{ID: account.ID, Name: account.Name, PlanType: normalizeOnboardingPlan(account.GetCredential("plan_type")), Status: account.Status, Public: accountPublicFallback(account)})
		}
		overview.Users = append(overview.Users, OnboardingAdminUser{UserID: user.ID, Email: user.Email, Username: user.Username, Discord: bindingMap[user.ID], Accounts: summaries, AccountUnits: units})
	}
	allAccounts, _, err := s.admin.ListAccounts(ctx, 1, 1000, PlatformOpenAI, AccountTypeOAuth, "", "", 0, "", "created_at", "asc")
	if err != nil {
		return nil, err
	}
	for _, account := range allAccounts {
		if accountOwnerID(account) != 0 {
			continue
		}
		overview.UnassignedAccounts = append(overview.UnassignedAccounts, OnboardingAccountSummary{ID: account.ID, Name: account.Name, PlanType: normalizeOnboardingPlan(account.GetCredential("plan_type")), Status: account.Status, Public: accountPublicFallback(account)})
	}
	return overview, nil
}
