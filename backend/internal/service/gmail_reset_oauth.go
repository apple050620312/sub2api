package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type gmailResetToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

func gmailResetHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}

func (s *OnboardingService) gmailResetCredentials(stored *gmailResetStored) (gmailResetCredentials, error) {
	if stored.CredentialsEncrypted == "" || s.encryptor == nil {
		return gmailResetCredentials{}, infraerrors.BadRequest("GMAIL_NOT_CONFIGURED", "请先导入 Gmail OAuth credentials JSON")
	}
	raw, err := s.encryptor.Decrypt(stored.CredentialsEncrypted)
	if err != nil {
		return gmailResetCredentials{}, fmt.Errorf("无法解密 Gmail OAuth 凭据，请检查持久化加密密钥")
	}
	return parseGmailResetCredentials(raw)
}

func (s *OnboardingService) StartGmailResetOAuth(ctx context.Context, userID int64) (string, error) {
	stored, err := s.loadGmailReset(ctx)
	if err != nil {
		return "", err
	}
	credentials, err := s.gmailResetCredentials(stored)
	if err != nil {
		return "", err
	}
	if stored.Config.RedirectURI == "" || !gmailResetURL(stored.Config.RedirectURI) {
		return "", infraerrors.BadRequest("GMAIL_REDIRECT_INVALID", "请先设置 Gmail OAuth 回调地址")
	}
	if len(credentials.RedirectURIs) > 0 {
		matched := false
		for _, redirect := range credentials.RedirectURIs {
			matched = matched || redirect == stored.Config.RedirectURI
		}
		if !matched {
			return "", infraerrors.BadRequest("GMAIL_REDIRECT_MISMATCH", "回调地址不在 credentials JSON 中，请在 Google Cloud 中添加相同地址并重新下载凭据")
		}
	}
	random := make([]byte, 64)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	state := "gmail-reset." + base64.RawURLEncoding.EncodeToString(random[:32])
	verifier := base64.RawURLEncoding.EncodeToString(random[32:])
	challenge := sha256.Sum256([]byte(verifier))
	encrypted, err := s.encryptor.Encrypt(verifier)
	if err != nil {
		return "", err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE service_pool_reset_gmail SET oauth_state_hash=$1,oauth_verifier_encrypted=$2,oauth_user_id=$3,oauth_expires_at=NOW()+INTERVAL '10 minutes' WHERE id=1 AND version=$4`, servicePoolResetHash(state), encrypted, userID, stored.Version)
	if err != nil {
		return "", err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return "", infraerrors.Conflict("GMAIL_CONFIG_CHANGED", "配置已变更，请刷新后重新授权")
	}
	params := url.Values{"client_id": {credentials.ClientID}, "redirect_uri": {stored.Config.RedirectURI}, "response_type": {"code"}, "scope": {gmailResetScope}, "access_type": {"offline"}, "prompt": {"consent"}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"}}
	return "https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode(), nil
}

func (s *OnboardingService) CompleteGmailResetOAuth(ctx context.Context, userID int64, code, state string) error {
	if code == "" || len(code) > 4096 || !strings.HasPrefix(state, "gmail-reset.") || len(state) > 200 {
		return infraerrors.BadRequest("GMAIL_OAUTH_INVALID", "Gmail 授权回调无效，请重新授权")
	}
	stored, err := s.loadGmailReset(ctx)
	if err != nil {
		return err
	}
	credentials, err := s.gmailResetCredentials(stored)
	if err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `WITH session AS MATERIALIZED (
SELECT oauth_verifier_encrypted FROM service_pool_reset_gmail WHERE id=1 AND oauth_state_hash=$1 AND oauth_user_id=$2 AND oauth_expires_at>NOW() AND version=$3 FOR UPDATE)
UPDATE service_pool_reset_gmail SET oauth_state_hash='',oauth_verifier_encrypted='' FROM session WHERE id=1 RETURNING session.oauth_verifier_encrypted`, servicePoolResetHash(state), userID, stored.Version)
	if err != nil {
		return err
	}
	var encrypted string
	if !rows.Next() {
		_ = rows.Close()
		return infraerrors.BadRequest("GMAIL_OAUTH_EXPIRED", "Gmail 授权已失效或已使用，请重新授权")
	}
	err = rows.Scan(&encrypted)
	_ = rows.Close()
	if err != nil {
		return err
	}
	verifier, err := s.encryptor.Decrypt(encrypted)
	if err != nil {
		return fmt.Errorf("无法解密 Gmail 授权会话，请重新授权")
	}
	token, err := gmailResetExchangeToken(ctx, gmailResetHTTPClient(30*time.Second), url.Values{"client_id": {credentials.ClientID}, "client_secret": {credentials.ClientSecret}, "code": {code}, "code_verifier": {verifier}, "grant_type": {"authorization_code"}, "redirect_uri": {stored.Config.RedirectURI}})
	if err != nil {
		return err
	}
	if token.RefreshToken == "" || !gmailResetHasScope(token.Scope) {
		return infraerrors.BadRequest("GMAIL_SCOPE_MISSING", "未取得 Gmail 只读权限或离线授权，请重新授权并允许读取邮件")
	}
	var profile struct {
		Email string `json:"emailAddress"`
	}
	if err := gmailResetGet(ctx, gmailResetHTTPClient(30*time.Second), token.AccessToken, "https://gmail.googleapis.com/gmail/v1/users/me/profile", &profile); err != nil {
		return err
	}
	if profile.Email == "" {
		return fmt.Errorf("Gmail 未返回邮箱地址，请重新授权")
	}
	refreshEncrypted, err := s.encryptor.Encrypt(token.RefreshToken)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `WITH connected AS (UPDATE service_pool_reset_gmail SET refresh_token_encrypted=$1,email=$2,monitor_since=NOW(),version=version+1 WHERE id=1 AND version=$3 RETURNING id)
UPDATE service_pool_reset_state SET next_check_at=NOW(),state=state #- '{gmail,pending}' #- '{status,pending_until}' WHERE id=1 AND EXISTS (SELECT 1 FROM connected)`, refreshEncrypted, strings.ToLower(profile.Email), stored.Version)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return infraerrors.Conflict("GMAIL_CONFIG_CHANGED", "配置已变更，请重新授权")
	}
	return nil
}

func gmailResetHasScope(scope string) bool {
	for _, granted := range strings.Fields(scope) {
		if granted == gmailResetScope {
			return true
		}
	}
	return false
}

func gmailResetExchangeToken(ctx context.Context, client *http.Client, params url.Values) (*gmailResetToken, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Gmail 授权令牌请求失败，请检查网络")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, infraerrors.BadRequest("GMAIL_TOKEN_FAILED", fmt.Sprintf("Gmail 授权令牌请求失败（HTTP %d），请检查凭据或重新授权", resp.StatusCode))
	}
	var token gmailResetToken
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&token); err != nil || token.AccessToken == "" {
		return nil, fmt.Errorf("Gmail 授权令牌响应无效")
	}
	return &token, nil
}

func gmailResetGet(ctx context.Context, client *http.Client, token, endpoint string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Gmail API 请求失败，请检查网络")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Gmail API 返回 HTTP %d，请检查授权与 API 配置", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(result); err != nil {
		return fmt.Errorf("Gmail API 响应格式无效")
	}
	return nil
}

func (s *OnboardingService) DisconnectGmailReset(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `WITH disconnected AS (UPDATE service_pool_reset_gmail SET refresh_token_encrypted='',email='',oauth_state_hash='',oauth_verifier_encrypted='',version=version+1 WHERE id=1 RETURNING id)
UPDATE service_pool_reset_state SET next_check_at=NOW(),state=state #- '{gmail,pending}' #- '{status,pending_until}' WHERE id=1 AND EXISTS (SELECT 1 FROM disconnected)`)
	return err
}
