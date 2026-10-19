package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type DiscordLoginProfile struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	Verified    bool   `json:"verified"`
	accessToken string
}

func discordLoginConfigured(stored *onboardingStoredConfig) bool {
	return stored != nil && stored.Discord.ClientID != "" && stored.Discord.ClientSecret != "" && stored.Discord.BotToken != "" && stored.Discord.GuildID != "" && strings.TrimSpace(stored.Discord.RedirectURI) != ""
}

func (s *OnboardingService) DiscordLoginReady(ctx context.Context) (bool, error) {
	stored, err := s.loadConfig(ctx)
	return discordLoginConfigured(stored), err
}

func (s *OnboardingService) StartDiscordLoginAuthorization(ctx context.Context, state string) (string, error) {
	stored, err := s.loadConfig(ctx)
	if err != nil {
		return "", err
	}
	if !discordLoginConfigured(stored) {
		return "", ErrDiscordNotConfigured
	}
	query := url.Values{"client_id": {stored.Discord.ClientID}, "response_type": {"code"}, "redirect_uri": {strings.TrimSpace(stored.Discord.RedirectURI)}, "scope": {"identify email guilds.join"}, "state": {state}}
	return "https://discord.com/oauth2/authorize?" + query.Encode(), nil
}

func (s *OnboardingService) exchangeDiscordAuthorization(ctx context.Context, stored *onboardingStoredConfig, code, redirectURI string) (*DiscordLoginProfile, error) {
	form := url.Values{"client_id": {stored.Discord.ClientID}, "client_secret": {stored.Discord.ClientSecret}, "grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://discord.com/api/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, discordNetworkError("兑换授权码", err)
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := decodeDiscordJSONResponse(resp, &token, "兑换授权码"); err != nil {
		return nil, err
	}
	if token.AccessToken == "" {
		return nil, fmt.Errorf("未收到 Discord 授权令牌，请重新授权")
	}
	userReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://discord.com/api/users/@me", nil)
	if err != nil {
		return nil, err
	}
	userReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
	userResp, err := s.httpClient.Do(userReq)
	if err != nil {
		return nil, discordNetworkError("读取用户资料", err)
	}
	var profile DiscordLoginProfile
	if err := decodeDiscordJSONResponse(userResp, &profile, "读取用户资料"); err != nil {
		return nil, err
	}
	if profile.ID == "" {
		return nil, fmt.Errorf("未收到 Discord 用户身份，请重新授权")
	}
	profile.accessToken = token.AccessToken
	return &profile, nil
}

func (s *OnboardingService) joinDiscordMember(ctx context.Context, stored *onboardingStoredConfig, profile *DiscordLoginProfile) error {
	joinURL := "https://discord.com/api/guilds/" + url.PathEscape(stored.Discord.GuildID) + "/members/" + url.PathEscape(profile.ID)
	joinBody, _ := json.Marshal(map[string]string{"access_token": profile.accessToken})
	joinReq, err := http.NewRequestWithContext(ctx, http.MethodPut, joinURL, strings.NewReader(string(joinBody)))
	if err != nil {
		return err
	}
	joinReq.Header.Set("Authorization", "Bot "+stored.Discord.BotToken)
	joinReq.Header.Set("Content-Type", "application/json")
	joinResp, err := s.httpClient.Do(joinReq)
	if err != nil {
		return discordNetworkError("加入服务器", err)
	}
	if err := decodeDiscordJSONResponse(joinResp, nil, "加入服务器"); err != nil {
		return fmt.Errorf("加入 Discord 服务器失败：%w", err)
	}
	if stored.Discord.RoleID != "" {
		roleReq, err := http.NewRequestWithContext(ctx, http.MethodPut, joinURL+"/roles/"+url.PathEscape(stored.Discord.RoleID), nil)
		if err != nil {
			return err
		}
		roleReq.Header.Set("Authorization", "Bot "+stored.Discord.BotToken)
		roleResp, err := s.httpClient.Do(roleReq)
		if err != nil {
			return discordNetworkError("授予身份组", err)
		}
		if err := decodeDiscordJSONResponse(roleResp, nil, "授予身份组"); err != nil {
			return fmt.Errorf("授予 Discord 身份组失败：%w", err)
		}
	}
	return nil
}

func (s *OnboardingService) CompleteDiscordLoginAuthorization(ctx context.Context, code string) (*DiscordLoginProfile, error) {
	stored, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !discordLoginConfigured(stored) {
		return nil, ErrDiscordNotConfigured
	}
	profile, err := s.exchangeDiscordAuthorization(ctx, stored, code, strings.TrimSpace(stored.Discord.RedirectURI))
	if err != nil {
		return nil, err
	}
	profile.Email = strings.ToLower(strings.TrimSpace(profile.Email))
	parsed, parseErr := mail.ParseAddress(profile.Email)
	if !profile.Verified || parseErr != nil || parsed.Address != profile.Email || len(profile.Email) > 255 {
		return nil, infraerrors.Forbidden("DISCORD_EMAIL_NOT_VERIFIED", "请先在 Discord 验证邮箱，再重新登录")
	}
	rows, err := s.client(ctx).QueryContext(ctx, `SELECT u.email FROM onboarding_discord_bindings b JOIN users u ON u.id=b.user_id AND u.deleted_at IS NULL WHERE b.discord_user_id=$1`, profile.ID)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("DISCORD_LOGIN_STORAGE_UNAVAILABLE", "Discord 登录存储暂时不可用，请稍后重试")
	}
	if rows.Next() {
		var existingEmail string
		err = rows.Scan(&existingEmail)
		if err != nil {
			return nil, infraerrors.ServiceUnavailable("DISCORD_LOGIN_STORAGE_UNAVAILABLE", "Discord 登录存储暂时不可用，请稍后重试")
		}
		if !strings.EqualFold(strings.TrimSpace(existingEmail), profile.Email) {
			err = infraerrors.Conflict("DISCORD_EMAIL_MISMATCH", "Discord 邮箱与站内用户邮箱不一致，请联系管理员修改用户邮箱后重试")
		}
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		if isDiscordApplicationError(err) {
			return nil, err
		}
		return nil, infraerrors.ServiceUnavailable("DISCORD_LOGIN_STORAGE_UNAVAILABLE", "Discord 登录存储暂时不可用，请稍后重试")
	}
	if err := s.joinDiscordMember(ctx, stored, profile); err != nil {
		return nil, err
	}
	profile.accessToken = ""
	return profile, nil
}

func (s *OnboardingService) BindDiscordLogin(ctx context.Context, userID int64, profile *DiscordLoginProfile) error {
	stored, err := s.loadConfig(ctx)
	if err != nil {
		return infraerrors.ServiceUnavailable("DISCORD_LOGIN_STORAGE_UNAVAILABLE", "Discord 登录配置暂时不可用，请稍后重试")
	}
	_, err = s.saveDiscordBinding(ctx, userID, profile.ID, profile.Username, stored)
	if err != nil {
		if isDiscordApplicationError(err) {
			return err
		}
		return infraerrors.ServiceUnavailable("DISCORD_LOGIN_STORAGE_UNAVAILABLE", "Discord 登录绑定暂时不可用，请稍后重试")
	}
	return err
}

func discordNetworkError(operation string, _ error) error {
	return infraerrors.ServiceUnavailable("DISCORD_API_UNAVAILABLE", fmt.Sprintf("Discord %s暂时不可用，请稍后重试", operation))
}

func isDiscordApplicationError(err error) bool {
	var applicationError *infraerrors.ApplicationError
	return errors.As(err, &applicationError)
}

func decodeDiscordJSONResponse(resp *http.Response, target any, operation string) error {
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return infraerrors.ServiceUnavailable("DISCORD_API_UNAVAILABLE", fmt.Sprintf("读取 Discord %s响应失败，请稍后重试", operation))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusTooManyRequests {
			return infraerrors.TooManyRequests("DISCORD_RATE_LIMITED", fmt.Sprintf("Discord %s请求过于频繁，请稍后重试", operation))
		}
		if resp.StatusCode >= 500 {
			return infraerrors.ServiceUnavailable("DISCORD_API_UNAVAILABLE", fmt.Sprintf("Discord %s暂时不可用，请稍后重试", operation))
		}
		return infraerrors.BadRequest("DISCORD_API_ERROR", fmt.Sprintf("Discord %s失败（HTTP %d），请检查机器人权限和应用配置", operation, resp.StatusCode))
	}
	if target == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, target); err != nil {
		return infraerrors.ServiceUnavailable("DISCORD_API_INVALID_RESPONSE", fmt.Sprintf("Discord %s返回了无效响应，请稍后重试", operation))
	}
	return nil
}
