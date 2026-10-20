package service

import (
	"context"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func (s *ServiceCenterService) checkOpenAIAccountNotAdded(ctx context.Context, token *OpenAITokenInfo) error {
	accountID := strings.TrimSpace(token.ChatGPTAccountID)
	userID := strings.TrimSpace(token.ChatGPTUserID)
	email := strings.ToLower(strings.TrimSpace(token.Email))
	if accountID == "" || (userID == "" && email == "") {
		return infraerrors.BadRequest("OPENAI_ACCOUNT_IDENTITY_MISSING", "无法确认 GPT 账号身份，请重新授权")
	}
	if _, err := s.client(ctx).ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "onboarding-openai:"+accountID); err != nil {
		return err
	}
	rows, err := s.client(ctx).QueryContext(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE deleted_at IS NULL AND platform = 'openai' AND type = 'oauth' AND credentials->>'chatgpt_account_id' = $1 AND (($2 <> '' AND credentials->>'chatgpt_user_id' = $2) OR ($3 <> '' AND LOWER(credentials->>'email') = $3)))`, accountID, userID, email)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return ErrOnboardingAccountMissing
	}
	var exists bool
	if err := rows.Scan(&exists); err != nil {
		return err
	}
	if exists {
		return infraerrors.Conflict("OPENAI_ACCOUNT_ALREADY_ADDED", "该 GPT 账号已接入，请使用已有账号或联系管理员分配")
	}
	return rows.Err()
}

func (s *ServiceCenterService) ownedAccount(ctx context.Context, userID, accountID int64) (*Account, error) {
	account, err := s.admin.GetAccount(ctx, accountID)
	if err != nil || account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth || !onboardingAccountBelongsToUser(account, userID) {
		return nil, ErrOnboardingAccountMissing
	}
	return account, nil
}

func (s *ServiceCenterService) SetOwnedAccountPaused(ctx context.Context, userID, accountID int64, paused bool) error {
	return s.mutateUser(ctx, userID, accountID, func(ctx context.Context) error {
		account, err := s.ownedAccount(ctx, userID, accountID)
		if err != nil {
			return err
		}
		wasPaused, _ := account.Extra[onboardingPausedExtraKey].(bool)
		if paused == wasPaused {
			return nil
		}
		status := StatusDisabled
		if !paused {
			if account.Status != StatusDisabled {
				return infraerrors.Conflict("ACCOUNT_RESUME_BLOCKED", "账号状态已被管理员更改，请联系管理员恢复")
			}
			status = StatusActive
		} else if account.Status != StatusActive {
			return infraerrors.Conflict("ACCOUNT_PAUSE_BLOCKED", "账号当前不可暂停，请联系管理员检查状态")
		}
		if err := s.admin.UpdateAccountExtra(ctx, accountID, map[string]any{onboardingPausedExtraKey: paused}); err != nil {
			return err
		}
		_, err = s.admin.UpdateAccount(ctx, accountID, &UpdateAccountInput{Status: status})
		return err
	})
}

func (s *ServiceCenterService) DeleteOwnedAccount(ctx context.Context, userID, accountID int64) error {
	return s.mutateUser(ctx, userID, accountID, func(ctx context.Context) error {
		if _, err := s.ownedAccount(ctx, userID, accountID); err != nil {
			return err
		}
		return s.admin.DeleteAccount(ctx, accountID)
	})
}
