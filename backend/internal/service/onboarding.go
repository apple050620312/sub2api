package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
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
	onboardingPausedExtraKey         = "onboarding_user_paused"
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
	Enabled             bool                                  `json:"enabled"`
	Discord             OnboardingDiscordConfig               `json:"discord"`
	PlanUnits           map[string]int                        `json:"plan_units"`
	SubscriptionRules   map[string]OnboardingSubscriptionRule `json:"subscription_rules"`
	OpenAIRedirectURI   string                                `json:"openai_redirect_uri"`
	CCSwitchDownloadURL string                                `json:"ccswitch_download_url"`
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
	ID            int64                      `json:"id"`
	Name          string                     `json:"name"`
	Email         string                     `json:"email"`
	PlanType      string                     `json:"plan_type"`
	Status        string                     `json:"status"`
	Schedulable   bool                       `json:"schedulable"`
	Paused        bool                       `json:"paused"`
	Concurrency   int                        `json:"concurrency"`
	CreatedAt     time.Time                  `json:"created_at"`
	UpdatedAt     time.Time                  `json:"updated_at"`
	LastUsedAt    *time.Time                 `json:"last_used_at,omitempty"`
	ExpiresAt     *time.Time                 `json:"expires_at,omitempty"`
	AuthExpiresAt *time.Time                 `json:"auth_expires_at,omitempty"`
	RateLimitAt   *time.Time                 `json:"rate_limit_reset_at,omitempty"`
	BlockedUntil  *time.Time                 `json:"blocked_until,omitempty"`
	Groups        []string                   `json:"groups"`
	Usage         Dynamic5hAccountDiagnostic `json:"usage"`
}

type OnboardingSubscriptionSummary struct {
	GroupName string    `json:"group_name"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at"`
}

type OnboardingUserOverview struct {
	Discord         *OnboardingDiscordBinding       `json:"discord,omitempty"`
	DiscordRequired bool                            `json:"discord_required"`
	Accounts        []OnboardingAccountSummary      `json:"accounts"`
	AccountUnits    int                             `json:"account_units"`
	ApiKeyCount     int64                           `json:"api_key_count"`
	ReadyForAccount bool                            `json:"ready_for_account"`
	ReadyForKey     bool                            `json:"ready_for_key"`
	ReadyForCCS     bool                            `json:"ready_for_ccs"`
	Config          OnboardingConfig                `json:"config"`
	Subscriptions   []OnboardingSubscriptionSummary `json:"subscriptions"`
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
	rows, err := s.client(ctx).QueryContext(ctx, `SELECT config FROM onboarding_config WHERE id = 1`)
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
		stored.Discord.ClientSecret, err = s.encryptor.Decrypt(stored.DiscordClientSecretEncrypted)
		if err != nil {
			return nil, fmt.Errorf("decrypt Discord client secret: %w", err)
		}
	}
	if stored.DiscordBotTokenEncrypted != "" && s.encryptor != nil {
		stored.Discord.BotToken, err = s.encryptor.Decrypt(stored.DiscordBotTokenEncrypted)
		if err != nil {
			return nil, fmt.Errorf("decrypt Discord bot token: %w", err)
		}
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
	for plan, units := range next.PlanUnits {
		if normalizeOnboardingPlan(plan) != plan || units < 0 || units > 1000 {
			return OnboardingConfig{}, infraerrors.BadRequest("ONBOARDING_CONFIG_INVALID", "套餐单位必须在 0 到 1000 之间")
		}
	}
	for _, rule := range next.SubscriptionRules {
		if rule.GroupID == 0 {
			continue
		}
		if rule.GroupID < 0 || rule.ValidityDays < 1 || rule.ValidityDays > MaxValidityDays || rule.Units < 1 || rule.Units > 1000 {
			return OnboardingConfig{}, infraerrors.BadRequest("ONBOARDING_CONFIG_INVALID", "订阅规则的有效期和消耗单位必须为正数")
		}
		group, err := s.admin.GetGroup(ctx, rule.GroupID)
		if err != nil || group == nil || group.Platform != PlatformOpenAI || group.SubscriptionType != SubscriptionTypeSubscription || group.Status != StatusActive {
			return OnboardingConfig{}, infraerrors.BadRequest("ONBOARDING_CONFIG_INVALID", "请选择有效的 OpenAI 订阅分组")
		}
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
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return OnboardingConfig{}, err
	}
	defer func() { _ = tx.Rollback() }()
	ctx = dbent.NewTxContext(ctx, tx)
	if _, err := tx.Client().ExecContext(ctx, `UPDATE onboarding_config SET config = $1::jsonb, updated_at = NOW() WHERE id = 1`, payload); err != nil {
		return OnboardingConfig{}, err
	}
	accounts, err := s.listOpenAIAccounts(ctx)
	if err != nil {
		return OnboardingConfig{}, err
	}
	owners := make(map[int64]struct{})
	for _, account := range accounts {
		if owner := accountOwnerID(account); owner > 0 {
			owners[owner] = struct{}{}
		}
	}
	rows, err := s.client(ctx).QueryContext(ctx, `SELECT DISTINCT user_id FROM user_subscriptions WHERE deleted_at IS NULL AND notes LIKE 'onboarding-managed:%'`)
	if err != nil {
		return OnboardingConfig{}, err
	}
	for rows.Next() {
		var owner int64
		if err := rows.Scan(&owner); err != nil {
			_ = rows.Close()
			return OnboardingConfig{}, err
		}
		owners[owner] = struct{}{}
	}
	rowsErr := rows.Err()
	_ = rows.Close()
	if rowsErr != nil {
		return OnboardingConfig{}, rowsErr
	}
	ownerIDs := make([]int64, 0, len(owners))
	for owner := range owners {
		ownerIDs = append(ownerIDs, owner)
	}
	sort.Slice(ownerIDs, func(first, second int) bool { return ownerIDs[first] < ownerIDs[second] })
	for _, owner := range ownerIDs {
		if _, err := tx.Client().ExecContext(ctx, `SELECT id FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, owner); err != nil {
			return OnboardingConfig{}, err
		}
		if err := s.reconcileSubscriptions(ctx, owner); err != nil {
			return OnboardingConfig{}, err
		}
	}
	if err := tx.Commit(); err != nil {
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
	return true
}

func onboardingAccountBelongsToUser(account *Account, userID int64) bool {
	return account != nil && userID > 0 && onboardingAccountOwnerUserID(account) == userID
}

func onboardingAccountAllowedForUser(account *Account, userID int64) bool {
	return onboardingAccountIsPublic(account)
}

func partitionOnboardingAccounts(accounts []*Account, userID int64) (owned, public []*Account) {
	for _, account := range accounts {
		if onboardingAccountBelongsToUser(account, userID) {
			owned = append(owned, account)
		} else if account != nil {
			public = append(public, account)
		}
	}
	return owned, public
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
	if units, ok := configured[plan]; ok {
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
	accounts, err := s.listOpenAIAccounts(ctx)
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
		if onboardingAccountContributes(account, time.Now()) {
			units += onboardingPlanUnits(account.GetCredential("plan_type"), stored.PlanUnits)
		}
	}
	return owned, units, nil
}

func (s *OnboardingService) client(ctx context.Context) *dbent.Client {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return s.db
}

func (s *OnboardingService) listOpenAIAccounts(ctx context.Context) ([]Account, error) {
	accounts := make([]Account, 0)
	for page := 1; ; page++ {
		batch, total, err := s.admin.ListAccounts(ctx, page, 200, PlatformOpenAI, AccountTypeOAuth, "", "", 0, "", "id", "asc")
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, batch...)
		if len(batch) == 0 || int64(len(accounts)) >= total {
			return accounts, nil
		}
	}
}

func onboardingAccountContributes(account Account, now time.Time) bool {
	paused, _ := account.Extra[onboardingPausedExtraKey].(bool)
	return account.Status == StatusActive && account.Schedulable && !paused &&
		(!account.AutoPauseOnExpired || account.ExpiresAt == nil || account.ExpiresAt.After(now))
}

func onboardingAccountSummary(account Account) OnboardingAccountSummary {
	paused, _ := account.Extra[onboardingPausedExtraKey].(bool)
	result := OnboardingAccountSummary{
		ID: account.ID, Name: account.Name, Email: account.GetCredential("email"),
		PlanType: normalizeOnboardingPlan(account.GetCredential("plan_type")), Status: account.Status,
		Schedulable: account.Schedulable, Paused: paused, Concurrency: account.Concurrency,
		CreatedAt: account.CreatedAt, UpdatedAt: account.UpdatedAt, LastUsedAt: account.LastUsedAt,
		ExpiresAt: account.ExpiresAt, RateLimitAt: account.RateLimitResetAt,
		BlockedUntil: account.TempUnschedulableUntil, Groups: []string{},
		Usage: dynamic5hAccountDiagnostic(&account, time.Now().UTC()),
	}
	for _, group := range account.Groups {
		if group != nil {
			result.Groups = append(result.Groups, group.Name)
		}
	}
	if raw := account.GetCredential("expires_at"); raw != "" {
		if expires, err := time.Parse(time.RFC3339, raw); err == nil {
			result.AuthExpiresAt = &expires
		} else if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
			expires := time.Unix(seconds, 0).UTC()
			result.AuthExpiresAt = &expires
		}
	}
	return result
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
		return nil, keyErr
	}
	summaries := make([]OnboardingAccountSummary, 0, len(accounts))
	for _, account := range accounts {
		summaries = append(summaries, onboardingAccountSummary(account))
	}
	subscriptions := make([]OnboardingSubscriptionSummary, 0)
	readyForKey := false
	if s.subscriptions != nil {
		subs, err := s.subscriptions.ListUserSubscriptions(ctx, userID)
		if err != nil {
			return nil, err
		}
		for _, sub := range subs {
			if sub.Group == nil || sub.Group.Platform != PlatformOpenAI {
				continue
			}
			subscriptions = append(subscriptions, OnboardingSubscriptionSummary{GroupName: sub.Group.Name, Status: sub.Status, ExpiresAt: sub.ExpiresAt})
			readyForKey = readyForKey || sub.IsActive()
		}
	}
	return &OnboardingUserOverview{
		Discord: binding, DiscordRequired: true, Accounts: summaries, AccountUnits: units,
		ApiKeyCount: keyCount, ReadyForAccount: binding != nil && binding.Status == "active",
		ReadyForKey: binding != nil && binding.Status == "active" && readyForKey,
		ReadyForCCS: keyCount > 0, Config: publicOnboardingConfig(stored),
		Subscriptions: subscriptions,
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
		return "", infraerrors.BadRequest("DISCORD_REDIRECT_URI_REQUIRED", "请先配置 Discord 回调地址")
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
		return fmt.Errorf("Discord 请求失败（HTTP %d），请检查应用配置、机器人权限和成员列表权限", resp.StatusCode)
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
		return nil, fmt.Errorf("Discord 未返回授权令牌，请重新授权")
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
		return nil, fmt.Errorf("Discord 未返回用户身份，请重新授权")
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
		return nil, fmt.Errorf("加入 Discord 服务器失败：%w", err)
	}
	if stored.Discord.RoleID != "" {
		roleReq, _ := http.NewRequestWithContext(ctx, http.MethodPut, joinURL+"/roles/"+url.PathEscape(stored.Discord.RoleID), nil)
		roleReq.Header.Set("Authorization", "Bot "+stored.Discord.BotToken)
		roleResp, err := s.httpClient.Do(roleReq)
		if err != nil {
			return nil, err
		}
		if err := decodeJSONResponse(roleResp, nil); err != nil {
			return nil, fmt.Errorf("授予 Discord 身份组失败：%w", err)
		}
	}
	return s.saveDiscordBinding(ctx, userID, discordUser.ID, discordUser.Username, stored)
}

func (s *OnboardingService) saveDiscordBinding(ctx context.Context, userID int64, discordID, username string, stored *onboardingStoredConfig) (*OnboardingDiscordBinding, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO onboarding_discord_bindings (user_id, discord_user_id, discord_username, guild_id, role_id, status, last_checked_at, updated_at) VALUES ($1,$2,$3,$4,$5,'active',NOW(),NOW()) ON CONFLICT (user_id) DO UPDATE SET discord_user_id=EXCLUDED.discord_user_id, discord_username=EXCLUDED.discord_username, guild_id=EXCLUDED.guild_id, role_id=EXCLUDED.role_id, status='active', last_checked_at=NOW(), updated_at=NOW()`, userID, discordID, username, stored.Discord.GuildID, stored.Discord.RoleID)
	if err != nil {
		var state interface{ SQLState() string }
		if errors.As(err, &state) && state.SQLState() == "23505" {
			return nil, infraerrors.Conflict("DISCORD_ALREADY_BOUND", "该 Discord 成员已绑定其他用户，请先核对现有归属")
		}
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
	extra := map[string]any{onboardingOwnerExtraKey: userID, "onboarding_owner_source": "user_oauth", "onboarding_plan_type": plan}
	var account *Account
	err = s.mutateUser(ctx, userID, 0, func(ctx context.Context) error {
		if err := s.checkOpenAIAccountNotAdded(ctx, tokenInfo); err != nil {
			return err
		}
		var createErr error
		account, createErr = s.admin.CreateAccount(ctx, &CreateAccountInput{Name: name, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: s.oauth.BuildAccountCredentials(tokenInfo), Extra: extra, Concurrency: 3, Priority: 50, SkipDefaultGroupBind: true})
		return createErr
	})
	if err != nil {
		return nil, err
	}
	return s.admin.GetAccount(ctx, account.ID)
}

func (s *OnboardingService) reconcileSubscriptions(ctx context.Context, userID int64) error {
	if dbent.TxFromContext(ctx) == nil {
		return s.mutateUser(ctx, userID, 0, nil)
	}
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
	selected := make(map[int64]OnboardingSubscriptionRule)
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
		if _, exists := selected[rule.GroupID]; exists {
			continue
		}
		selected[rule.GroupID] = rule
		remaining -= cost
	}
	if _, err := s.client(ctx).ExecContext(ctx, `SELECT id FROM user_subscriptions WHERE user_id=$1 AND deleted_at IS NULL ORDER BY id FOR UPDATE`, userID); err != nil {
		return err
	}
	subs, err := s.subscriptions.userSubRepo.ListByUserID(ctx, userID)
	if err != nil {
		return err
	}
	activeGroups, err := s.syncOnboardingSubscriptions(ctx, userID, selected, subs, time.Now())
	if err != nil {
		return err
	}
	groupIDs := make([]int64, 0, len(activeGroups))
	for id := range activeGroups {
		groupIDs = append(groupIDs, id)
	}
	sort.Slice(groupIDs, func(first, second int) bool { return groupIDs[first] < groupIDs[second] })
	for _, account := range accounts {
		if !onboardingAccountContributes(account, time.Now()) {
			continue
		}
		managedGroups := make(map[int64]struct{})
		for _, sub := range subs {
			if strings.HasPrefix(sub.Notes, onboardingManagedNotes+":") {
				managedGroups[sub.GroupID] = struct{}{}
			}
		}
		ids := append([]int64{}, groupIDs...)
		for _, groupID := range account.GroupIDs {
			if _, managed := managedGroups[groupID]; managed {
				continue
			}
			if _, selected := activeGroups[groupID]; !selected {
				ids = append(ids, groupID)
			}
		}
		if _, err := s.admin.UpdateAccount(ctx, account.ID, &UpdateAccountInput{GroupIDs: &ids, SkipMixedChannelCheck: true}); err != nil {
			return err
		}
	}
	return nil
}

func (s *OnboardingService) syncOnboardingSubscriptions(ctx context.Context, userID int64, selected map[int64]OnboardingSubscriptionRule, subs []UserSubscription, now time.Time) (map[int64]struct{}, error) {
	activeGroups := make(map[int64]struct{})
	existing := make(map[int64]UserSubscription, len(subs))
	for _, sub := range subs {
		existing[sub.GroupID] = sub
	}
	changed := make(map[int64]struct{})
	for _, sub := range subs {
		if !strings.HasPrefix(sub.Notes, onboardingManagedNotes+":") {
			continue
		}
		_, eligible := selected[sub.GroupID]
		status := sub.Status
		notes := sub.Notes
		if !eligible && status == SubscriptionStatusActive {
			status = SubscriptionStatusSuspended
			notes = strings.TrimSuffix(notes, ":account_paused") + ":account_paused"
		} else if eligible && status == SubscriptionStatusSuspended && strings.HasSuffix(notes, ":account_paused") && now.Before(sub.ExpiresAt) {
			status = SubscriptionStatusActive
			notes = strings.TrimSuffix(notes, ":account_paused")
		}
		if status != sub.Status {
			if err := s.subscriptions.userSubRepo.UpdateStatus(ctx, sub.ID, status); err != nil {
				return nil, err
			}
			if err := s.subscriptions.userSubRepo.UpdateNotes(ctx, sub.ID, notes); err != nil {
				return nil, err
			}
			changed[sub.GroupID] = struct{}{}
		}
		sub.Status, sub.Notes = status, notes
		existing[sub.GroupID] = sub
	}
	for groupID, rule := range selected {
		sub, exists := existing[groupID]
		if !exists {
			created, err := s.subscriptions.createSubscription(ctx, &AssignSubscriptionInput{UserID: userID, GroupID: groupID, ValidityDays: rule.ValidityDays, Notes: onboardingManagedNotes + ":account"})
			if err != nil {
				return nil, err
			}
			sub = *created
			changed[groupID] = struct{}{}
		}
		if sub.Status == SubscriptionStatusActive && !now.Before(sub.StartsAt) && now.Before(sub.ExpiresAt) {
			activeGroups[groupID] = struct{}{}
		}
	}
	invalidate := func() {
		for groupID := range changed {
			_ = s.subscriptions.invalidateSubscriptionCaches(userID, groupID)
		}
	}
	if tx := dbent.TxFromContext(ctx); tx != nil {
		tx.OnCommit(func(next dbent.Committer) dbent.Committer {
			return dbent.CommitFunc(func(commitCtx context.Context, tx *dbent.Tx) error {
				if err := next.Commit(commitCtx, tx); err != nil {
					return err
				}
				invalidate()
				return nil
			})
		})
	} else {
		invalidate()
	}
	return activeGroups, nil
}

func (s *OnboardingService) mutateUser(ctx context.Context, userID, accountID int64, mutate func(context.Context) error) error {
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	ctx = dbent.NewTxContext(ctx, tx)
	if _, err := tx.Client().ExecContext(ctx, `SELECT id FROM onboarding_config WHERE id=1 FOR SHARE`); err != nil {
		return err
	}
	if _, err := tx.Client().ExecContext(ctx, `SELECT id FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, userID); err != nil {
		return err
	}
	if accountID > 0 {
		if _, err := tx.Client().ExecContext(ctx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, accountID); err != nil {
			return err
		}
	}
	if mutate != nil {
		if err := mutate(ctx); err != nil {
			return err
		}
	}
	if err := s.reconcileSubscriptions(ctx, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *OnboardingService) BindExistingAccount(ctx context.Context, userID, accountID int64) error {
	return s.mutateUser(ctx, userID, accountID, func(ctx context.Context) error {
		if _, err := s.admin.GetUser(ctx, userID); err != nil {
			return err
		}
		if _, err := s.requireDiscord(ctx, userID); err != nil {
			return err
		}
		account, err := s.admin.GetAccount(ctx, accountID)
		if err != nil || account == nil {
			return ErrOnboardingAccountMissing
		}
		if account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth {
			return infraerrors.BadRequest("ONBOARDING_ACCOUNT_INVALID", "只能分配通过 OpenAI OAuth 接入的账号")
		}
		if owner := accountOwnerID(*account); owner != 0 && owner != userID {
			return infraerrors.Conflict("ONBOARDING_ACCOUNT_ASSIGNED", "账号已分配给其他用户，请先核对现有归属")
		}
		return s.admin.UpdateAccountExtra(ctx, accountID, map[string]any{onboardingOwnerExtraKey: userID, "onboarding_owner_source": "admin_assignment"})
	})
}

func (s *OnboardingService) UnbindAccount(ctx context.Context, accountID int64) error {
	account, err := s.admin.GetAccount(ctx, accountID)
	if err != nil || account == nil {
		return ErrOnboardingAccountMissing
	}
	owner := accountOwnerID(*account)
	if owner <= 0 {
		return nil
	}
	return s.mutateUser(ctx, owner, accountID, func(ctx context.Context) error {
		current, err := s.admin.GetAccount(ctx, accountID)
		if err != nil || current == nil || accountOwnerID(*current) != owner {
			return ErrOnboardingAccountMissing
		}
		return s.admin.UpdateAccountExtra(ctx, accountID, map[string]any{onboardingOwnerExtraKey: nil, onboardingPublicFallbackExtraKey: nil, "onboarding_owner_source": nil})
	})
}

func (s *OnboardingService) AdminOverview(ctx context.Context) (*OnboardingAdminOverview, error) {
	stored, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	users := make([]User, 0)
	for page := 1; ; page++ {
		batch, total, err := s.admin.ListUsers(ctx, page, 200, UserListFilters{}, "id", "asc")
		if err != nil {
			return nil, err
		}
		users = append(users, batch...)
		if len(batch) == 0 || int64(len(users)) >= total {
			break
		}
	}
	allAccounts, err := s.listOpenAIAccounts(ctx)
	if err != nil {
		return nil, err
	}
	accountsByOwner := make(map[int64][]OnboardingAccountSummary)
	unitsByOwner := make(map[int64]int)
	for _, account := range allAccounts {
		owner := accountOwnerID(account)
		accountsByOwner[owner] = append(accountsByOwner[owner], onboardingAccountSummary(account))
		if onboardingAccountContributes(account, time.Now()) {
			unitsByOwner[owner] += onboardingPlanUnits(account.GetCredential("plan_type"), stored.PlanUnits)
		}
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
		summaries := accountsByOwner[user.ID]
		if summaries == nil {
			summaries = []OnboardingAccountSummary{}
		}
		overview.Users = append(overview.Users, OnboardingAdminUser{UserID: user.ID, Email: user.Email, Username: user.Username, Discord: bindingMap[user.ID], Accounts: summaries, AccountUnits: unitsByOwner[user.ID]})
	}
	if accountsByOwner[0] != nil {
		overview.UnassignedAccounts = accountsByOwner[0]
	}
	return overview, nil
}
