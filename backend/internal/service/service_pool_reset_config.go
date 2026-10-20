package service

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type ServicePoolResetConfig struct {
	Enabled   bool   `json:"enabled"`
	ChannelID string `json:"channel_id"`
	Message   string `json:"message"`
}

type ServicePoolResetStatus struct {
	CheckedAt             *time.Time `json:"checked_at,omitempty"`
	Phase                 string     `json:"phase"`
	LastError             string     `json:"last_error,omitempty"`
	PendingUntil          *time.Time `json:"pending_until,omitempty"`
	CooldownUntil         *time.Time `json:"cooldown_until,omitempty"`
	LastResetAt           *time.Time `json:"last_reset_at,omitempty"`
	LastResetKind         string     `json:"last_reset_kind,omitempty"`
	AffectedSubscriptions int        `json:"affected_subscriptions"`
	NotificationState     string     `json:"notification_state,omitempty"`
}

func validateServicePoolResetConfig(next ServicePoolResetConfig, discord, current OnboardingDiscordConfig) error {
	if utf8.RuneCountInString(next.Message) > 2000 {
		return infraerrors.BadRequest("POOL_RESET_CONFIG_INVALID", "Discord 通知内容最多 2000 个字符")
	}
	if !next.Enabled {
		return nil
	}
	channel, err := strconv.ParseUint(next.ChannelID, 10, 64)
	if err != nil || channel == 0 || strings.TrimSpace(next.Message) == "" || discord.GuildID == "" || (discord.BotToken == "" && current.BotToken == "") {
		return infraerrors.BadRequest("POOL_RESET_CONFIG_INVALID", "启用前请配置 Discord 机器人、服务器、通知频道和通知内容")
	}
	return nil
}

type servicePoolResetState struct {
	Monitor     resetMonitorState      `json:"monitor"`
	LastResetAt time.Time              `json:"last_reset_at"`
	Status      ServicePoolResetStatus `json:"status"`
}

type servicePoolResetDecision struct {
	Key, Kind       string
	Observed, Total int
}

func servicePoolResetHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
