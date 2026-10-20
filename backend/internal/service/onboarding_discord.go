package service

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type OnboardingDiscordMember struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	BoundEmail  string `json:"bound_email,omitempty"`
}

type onboardingDiscordGuildMember struct {
	Nick string `json:"nick"`
	User struct {
		ID         string `json:"id"`
		Username   string `json:"username"`
		GlobalName string `json:"global_name"`
		Bot        bool   `json:"bot"`
	} `json:"user"`
}

func (s *ServiceCenterService) discordBotRequest(ctx context.Context, stored *serviceCenterStoredConfig, method, path string, target any) error {
	if stored.Discord.BotToken == "" || stored.Discord.GuildID == "" {
		return ErrDiscordNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://discord.com/api/guilds/"+url.PathEscape(stored.Discord.GuildID)+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+stored.Discord.BotToken)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	return decodeJSONResponse(resp, target)
}

func (s *ServiceCenterService) ListDiscordMembers(ctx context.Context, query string) ([]OnboardingDiscordMember, error) {
	stored, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	path := "/members?limit=100"
	if query != "" {
		path = "/members/search?" + url.Values{"query": {query}, "limit": {"100"}}.Encode()
	}
	var members []onboardingDiscordGuildMember
	if err := s.discordBotRequest(ctx, stored, http.MethodGet, path, &members); err != nil {
		return nil, err
	}
	rows, err := s.client(ctx).QueryContext(ctx, `SELECT b.discord_user_id, u.email FROM onboarding_discord_bindings b JOIN users u ON u.id = b.user_id AND u.deleted_at IS NULL WHERE b.guild_id = $1`, stored.Discord.GuildID)
	if err != nil {
		return nil, err
	}
	bound := make(map[string]string)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var memberID, email string
		if err := rows.Scan(&memberID, &email); err != nil {
			return nil, err
		}
		bound[memberID] = email
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]OnboardingDiscordMember, 0, len(members))
	for _, member := range members {
		if member.User.Bot {
			continue
		}
		displayName := member.Nick
		if displayName == "" {
			displayName = member.User.GlobalName
		}
		if displayName == "" {
			displayName = member.User.Username
		}
		result = append(result, OnboardingDiscordMember{ID: member.User.ID, Username: member.User.Username, DisplayName: displayName, BoundEmail: bound[member.User.ID]})
	}
	return result, nil
}

func (s *ServiceCenterService) BindDiscordMember(ctx context.Context, userID int64, memberID string) (*OnboardingDiscordBinding, error) {
	if userID <= 0 || memberID == "" || strings.Trim(memberID, "0123456789") != "" {
		return nil, infraerrors.BadRequest("DISCORD_MEMBER_INVALID", "请选择有效的 Discord 成员")
	}
	if _, err := s.admin.GetUser(ctx, userID); err != nil {
		return nil, err
	}
	stored, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	var member onboardingDiscordGuildMember
	path := "/members/" + url.PathEscape(memberID)
	if err := s.discordBotRequest(ctx, stored, http.MethodGet, path, &member); err != nil {
		return nil, err
	}
	if member.User.ID != memberID || member.User.Bot {
		return nil, infraerrors.BadRequest("DISCORD_MEMBER_INVALID", "请选择该服务器中的普通成员")
	}
	bindings, err := s.listBindings(ctx)
	if err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		if binding.DiscordUserID == memberID && binding.UserID != userID {
			return nil, infraerrors.Conflict("DISCORD_ALREADY_BOUND", "该 Discord 成员已绑定其他用户，请先核对现有归属")
		}
	}
	if stored.Discord.RoleID != "" {
		if err := s.discordBotRequest(ctx, stored, http.MethodPut, path+"/roles/"+url.PathEscape(stored.Discord.RoleID), nil); err != nil {
			return nil, err
		}
	}
	return s.saveDiscordBinding(ctx, userID, memberID, member.User.Username, stored)
}
