package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type OnboardingDiscordChannel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type int    `json:"type"`
}

func (s *ServiceCenterService) ListDiscordChannels(ctx context.Context) ([]OnboardingDiscordChannel, error) {
	stored, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	var channels []OnboardingDiscordChannel
	if err := s.discordBotRequest(ctx, stored, http.MethodGet, "/channels", &channels); err != nil {
		return nil, err
	}
	result := make([]OnboardingDiscordChannel, 0)
	for _, channel := range channels {
		if channel.Type == 0 || channel.Type == 5 {
			result = append(result, channel)
		}
	}
	return result, nil
}

func (s *ServicePoolResetService) flushResetNotifications(ctx context.Context) error {
	stored, err := s.onboarding.loadConfig(ctx)
	if err != nil {
		return err
	}
	rows, err := s.onboarding.db.QueryContext(ctx, `UPDATE service_pool_reset_events e SET notification_state='sending'
WHERE e.id=(SELECT id FROM service_pool_reset_events p WHERE p.notification_state='pending'
AND NOT EXISTS (SELECT 1 FROM service_pool_reset_cache_targets t WHERE t.event_id=p.id)
ORDER BY reset_at LIMIT 1 FOR UPDATE SKIP LOCKED) RETURNING id,notification_channel_id,notification_content`)
	if err != nil {
		return err
	}
	var id, channel, content string
	if !rows.Next() {
		rowsErr := rows.Err()
		_ = rows.Close()
		return rowsErr
	}
	err = rows.Scan(&id, &channel, &content)
	_ = rows.Close()
	if err != nil {
		return err
	}
	state, messageID, errorCode := s.sendResetNotification(ctx, stored, id, channel, content)
	_, err = s.onboarding.db.ExecContext(ctx, `UPDATE service_pool_reset_events SET notification_state=$2,notification_message_id=$3,notification_error=$4 WHERE id=$1`, id, state, messageID, errorCode)
	return err
}

func (s *ServicePoolResetService) sendResetNotification(ctx context.Context, stored *serviceCenterStoredConfig, nonce, channel, content string) (string, string, string) {
	if stored.Discord.BotToken == "" {
		return "failed", "", "bot_not_configured"
	}
	payload, err := json.Marshal(map[string]any{"content": content, "nonce": nonce, "enforce_nonce": true, "allowed_mentions": map[string]any{"parse": []string{"everyone", "users", "roles"}}})
	if err != nil {
		return "failed", "", "invalid_message"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://discord.com/api/v10/channels/"+url.PathEscape(channel)+"/messages", bytes.NewReader(payload))
	if err != nil {
		return "failed", "", "invalid_request"
	}
	req.Header.Set("Authorization", "Bot "+stored.Discord.BotToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.onboarding.httpClient.Do(req)
	if err != nil {
		return "uncertain", "", "delivery_unknown"
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusTooManyRequests {
		return "pending", "", "discord_rate_limited"
	}
	if resp.StatusCode >= 500 {
		return "uncertain", "", "discord_server_error"
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "failed", "", "discord_http_" + strconv.Itoa(resp.StatusCode)
	}
	var message struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&message); err != nil || strings.TrimSpace(message.ID) == "" {
		return "uncertain", "", "delivery_unknown"
	}
	return "sent", message.ID, ""
}

func (s *ServiceCenterService) validateResetChannel(ctx context.Context, cfg ServicePoolResetConfig, discord OnboardingDiscordConfig) error {
	if !cfg.Enabled {
		return nil
	}
	var channels []OnboardingDiscordChannel
	stored := &serviceCenterStoredConfig{ServiceCenterConfig: ServiceCenterConfig{Discord: discord}}
	if err := s.discordBotRequest(ctx, stored, http.MethodGet, "/channels", &channels); err != nil {
		return fmt.Errorf("无法读取 Discord 通知频道，请检查机器人和服务器配置：%w", err)
	}
	for _, channel := range channels {
		if channel.ID == cfg.ChannelID && (channel.Type == 0 || channel.Type == 5) {
			return nil
		}
	}
	return fmt.Errorf("请选择当前 Discord 服务器中的文字频道")
}
