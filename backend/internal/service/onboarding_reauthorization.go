package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

var ErrOnboardingReauthorizationIdentity = infraerrors.BadRequest("OPENAI_REAUTH_IDENTITY_MISMATCH", "授权身份与原 GPT 账号不一致或无法确认，请使用原账号及原工作区重新授权")

func onboardingReauthorizationKind(accountID int64, sessionID string) string {
	return fmt.Sprintf("openai-reauth:%d:%s", accountID, sessionID)
}

func validateOnboardingReauthorizationIdentity(account *Account, token *OpenAITokenInfo) error {
	if account == nil || token == nil {
		return ErrOnboardingReauthorizationIdentity
	}
	accountID := strings.TrimSpace(account.GetCredential("chatgpt_account_id"))
	if accountID == "" || accountID != strings.TrimSpace(token.ChatGPTAccountID) {
		return ErrOnboardingReauthorizationIdentity
	}
	if userID := strings.TrimSpace(account.GetCredential("chatgpt_user_id")); userID != "" {
		if userID == strings.TrimSpace(token.ChatGPTUserID) {
			return nil
		}
		return ErrOnboardingReauthorizationIdentity
	}
	if email := strings.TrimSpace(account.GetCredential("email")); email != "" && strings.EqualFold(email, strings.TrimSpace(token.Email)) {
		return nil
	}
	return ErrOnboardingReauthorizationIdentity
}

func (s *OnboardingService) StartOwnedAccountReauthorization(ctx context.Context, userID, accountID int64) (*OnboardingOAuthStart, error) {
	stored, err := s.requireDiscord(ctx, userID)
	if err != nil {
		return nil, err
	}
	account, err := s.ownedAccount(ctx, userID, accountID)
	if err != nil || account.IsCredentialShadow() {
		return nil, ErrOnboardingAccountMissing
	}
	redirectURI := strings.TrimSpace(stored.OpenAIRedirectURI)
	if redirectURI == "" {
		redirectURI = openai.DefaultRedirectURI
	}
	result, err := s.oauth.GenerateAuthURL(ctx, account.ProxyID, redirectURI, PlatformOpenAI)
	if err != nil {
		return nil, err
	}
	state, err := s.signState(userID, onboardingReauthorizationKind(accountID, result.SessionID))
	if err != nil {
		return nil, err
	}
	return &OnboardingOAuthStart{AuthURL: result.AuthURL, SessionID: result.SessionID, OnboardingState: state, RedirectURI: redirectURI}, nil
}

func (s *OnboardingService) CompleteOwnedAccountReauthorization(ctx context.Context, userID, accountID int64, sessionID, code, oauthState, onboardingState string) (*Account, error) {
	if err := s.verifyState(onboardingState, onboardingReauthorizationKind(accountID, sessionID), userID); err != nil {
		return nil, err
	}
	if _, err := s.requireDiscord(ctx, userID); err != nil {
		return nil, err
	}
	account, err := s.ownedAccount(ctx, userID, accountID)
	if err != nil || account.IsCredentialShadow() {
		return nil, ErrOnboardingAccountMissing
	}
	token, err := s.oauth.ExchangeCode(ctx, &OpenAIExchangeCodeInput{SessionID: sessionID, Code: code, State: oauthState, ProxyID: account.ProxyID})
	if err != nil {
		return nil, err
	}
	return s.applyOwnedAccountReauthorization(ctx, userID, accountID, token)
}

func (s *OnboardingService) applyOwnedAccountReauthorization(ctx context.Context, userID, accountID int64, token *OpenAITokenInfo) (*Account, error) {
	var updated *Account
	err := s.mutateUser(ctx, userID, accountID, func(ctx context.Context) error {
		account, err := s.ownedAccount(ctx, userID, accountID)
		if err != nil || account.IsCredentialShadow() {
			return ErrOnboardingAccountMissing
		}
		if err := validateOnboardingReauthorizationIdentity(account, token); err != nil {
			return err
		}
		if strings.TrimSpace(token.AccessToken) == "" || strings.TrimSpace(token.RefreshToken) == "" {
			return infraerrors.BadRequest("OPENAI_REAUTH_TOKEN_MISSING", "授权未返回完整令牌，请重新开始授权")
		}
		status := account.Status
		if status == StatusError {
			status = StatusActive
		}
		if paused, _ := account.Extra[onboardingPausedExtraKey].(bool); paused {
			status = StatusDisabled
		}
		credentials := SanitizeStoredCredentials(account.Platform, MergeCredentials(account.Credentials, s.oauth.BuildAccountCredentials(token)))
		if _, err := s.client(ctx).ExecContext(ctx, `UPDATE accounts SET error_message='' WHERE id=$1 AND deleted_at IS NULL`, accountID); err != nil {
			return err
		}
		updated, err = s.admin.UpdateAccount(ctx, accountID, &UpdateAccountInput{Credentials: credentials, Status: status})
		return err
	})
	if err != nil {
		return nil, err
	}
	if s.tokenCacheInvalidator != nil {
		invalidateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.tokenCacheInvalidator.InvalidateToken(invalidateCtx, updated); err != nil {
			slog.Warn("onboarding.reauthorize.invalidate_token_failed", "account_id", accountID, "err", err)
		}
	}
	return updated, nil
}
