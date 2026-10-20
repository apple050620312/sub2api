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
	"time"
	"unicode/utf8"
)

type resetMonitorMessage struct {
	ID              string     `json:"id"`
	ChannelID       string     `json:"channel_id"`
	Content         string     `json:"content"`
	Timestamp       time.Time  `json:"timestamp"`
	EditedTimestamp *time.Time `json:"edited_timestamp"`
	WebhookID       string     `json:"webhook_id"`
	Author          struct {
		ID  string `json:"id"`
		Bot bool   `json:"bot"`
	} `json:"author"`
	Embeds []struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Fields      []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"fields"`
		Footer struct {
			Text string `json:"text"`
		} `json:"footer"`
		Author struct {
			Name string `json:"name"`
		} `json:"author"`
	} `json:"embeds"`
}

type resetMonitorMessageRecord struct {
	MessageID  string    `json:"message_id"`
	ReceivedAt time.Time `json:"received_at"`
	Sender     string    `json:"sender"`
	Subject    string    `json:"subject"`
	Kind       string    `json:"kind"`
	Confidence float64   `json:"confidence"`
	Evidence   string    `json:"evidence"`
	Outcome    string    `json:"outcome"`
	Attempts   int       `json:"attempts"`
	Body       string    `json:"-"`
	Key        string    `json:"-"`
}

type resetMonitorAnalysis struct {
	Kind       string  `json:"kind"`
	Confidence float64 `json:"confidence"`
	Evidence   string  `json:"evidence"`
}

type resetMonitorRateLimitError struct{ RetryAfter time.Duration }

func (err *resetMonitorRateLimitError) Error() string {
	return "Discord 请求达到频率限制，稍后重试"
}

func resetMonitorHTTPClient(timeout time.Duration, loopback bool) *http.Client {
	transport := http.DefaultTransport
	if native, ok := transport.(*http.Transport); ok && loopback {
		cloned := native.Clone()
		cloned.Proxy = nil
		transport = cloned
	}
	return &http.Client{Timeout: timeout, Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}

func resetMonitorGet(ctx context.Context, client *http.Client, token, endpoint string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bot "+token)
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("无法读取 Discord 频道，请检查机器人及网络")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusTooManyRequests {
		var limit struct {
			RetryAfter float64 `json:"retry_after"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&limit)
		if !(limit.RetryAfter >= 5 && limit.RetryAfter <= 3600) {
			limit.RetryAfter = 60
		}
		return &resetMonitorRateLimitError{RetryAfter: time.Duration(limit.RetryAfter * float64(time.Second))}
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("读取 Discord 频道返回 HTTP %d，请检查查看频道和读取历史消息权限", response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4*1024*1024)).Decode(target); err != nil {
		return fmt.Errorf("无法解析 Discord 消息响应")
	}
	return nil
}

func resetMonitorMessages(ctx context.Context, client *http.Client, token, guild string, stored *resetMonitorStored, now time.Time) ([]resetMonitorMessage, error) {
	endpoint := "https://discord.com/api/v10/channels/" + url.PathEscape(stored.Config.ChannelID)
	var channel struct {
		GuildID string `json:"guild_id"`
	}
	if err := resetMonitorGet(ctx, client, token, endpoint, &channel); err != nil {
		return nil, err
	}
	if channel.GuildID == "" || channel.GuildID != guild {
		return nil, fmt.Errorf("监测频道不属于配置的 Discord 服务器")
	}
	since := now.Add(-time.Duration(stored.Config.MaxAgeHours) * time.Hour)
	if stored.Since.After(since) {
		since = stored.Since
	}
	var messages []resetMonitorMessage
	before := ""
	for range 10 {
		params := url.Values{"limit": {"100"}}
		if before != "" {
			params.Set("before", before)
		}
		var batch []resetMonitorMessage
		if err := resetMonitorGet(ctx, client, token, endpoint+"/messages?"+params.Encode(), &batch); err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			return messages, nil
		}
		for _, message := range batch {
			if message.ChannelID != stored.Config.ChannelID || !resetMonitorSnowflake(message.ID) || message.Timestamp.IsZero() {
				continue
			}
			messages = append(messages, message)
		}
		last := batch[len(batch)-1]
		if len(batch) < 100 || (!last.Timestamp.IsZero() && last.Timestamp.Before(since)) {
			return messages, nil
		}
		if last.ID == before || !resetMonitorSnowflake(last.ID) {
			return nil, fmt.Errorf("无法解析 Discord 消息分页")
		}
		before = last.ID
	}
	return nil, fmt.Errorf("监测时段内的频道消息超过 1000 条，请缩短消息时效或使用专用通知频道")
}

func resetMonitorRecord(message resetMonitorMessage, cfg ResetMonitorConfig) (resetMonitorMessageRecord, bool) {
	if cfg.RequireEmbeds && len(message.Embeds) == 0 {
		return resetMonitorMessageRecord{}, false
	}
	if cfg.BotsOnly && !message.Author.Bot && message.WebhookID == "" {
		return resetMonitorMessageRecord{}, false
	}
	if len(cfg.AuthorIDs) > 0 {
		allowed := false
		for _, author := range cfg.AuthorIDs {
			allowed = allowed || message.Author.ID == author || message.WebhookID == author
		}
		if !allowed {
			return resetMonitorMessageRecord{}, false
		}
	}
	parts := []string{message.Content}
	title := ""
	for _, embed := range message.Embeds {
		if title == "" {
			title = embed.Title
		}
		parts = append(parts, embed.Title, embed.Description, embed.Author.Name)
		for _, field := range embed.Fields {
			parts = append(parts, field.Name, field.Value)
		}
		parts = append(parts, embed.Footer.Text)
	}
	body := strings.TrimSpace(strings.Join(parts, "\n"))
	if body == "" {
		return resetMonitorMessageRecord{}, false
	}
	if title == "" {
		title = message.Content
	}
	received := message.Timestamp
	if message.EditedTimestamp != nil && message.EditedTimestamp.After(received) {
		received = *message.EditedTimestamp
	}
	return resetMonitorMessageRecord{MessageID: message.ID, ReceivedAt: received, Sender: message.Author.ID, Subject: resetMonitorTruncate(title, 500), Kind: "unknown", Outcome: "untrusted_source", Body: resetMonitorTruncate(body, cfg.MaxBodyChars), Key: servicePoolResetHash(message.ChannelID + ":" + message.ID + ":" + body)}, true
}

func resetMonitorTruncate(value string, limit int) string {
	characters := []rune(strings.ToValidUTF8(value, ""))
	if len(characters) > limit {
		characters = characters[:limit]
	}
	return string(characters)
}

func (s *ServicePoolResetService) analyzeResetMessage(ctx context.Context, stored *resetMonitorStored, record resetMonitorMessageRecord) (*resetMonitorAnalysis, error) {
	key, err := s.onboarding.db.APIKey.Get(ctx, stored.Config.ModelAPIKeyID)
	if err != nil || key.UserID != stored.OwnerUserID || key.DeletedAt != nil || key.Status != "active" || (key.ExpiresAt != nil && !key.ExpiresAt.After(time.Now())) {
		return nil, fmt.Errorf("监测使用的站内 API Key 不可用，请重新选择")
	}
	port := 8080
	if s.onboarding.cfg != nil && s.onboarding.cfg.Server.Port > 0 {
		port = s.onboarding.cfg.Server.Port
	}
	return resetMonitorAnalyze(ctx, resetMonitorHTTPClient(time.Duration(stored.Config.ModelTimeoutSeconds)*time.Second, true), stored.Config, "http://127.0.0.1:"+strconv.Itoa(port)+"/v1", key.Key, record)
}

func resetMonitorAnalyze(ctx context.Context, client *http.Client, cfg ResetMonitorConfig, baseURL, key string, record resetMonitorMessageRecord) (*resetMonitorAnalysis, error) {
	data, err := json.Marshal(map[string]string{"author_id": record.Sender, "received_at": record.ReceivedAt.UTC().Format(time.RFC3339), "title": record.Subject, "body": record.Body})
	if err != nil {
		return nil, err
	}
	prompt := defaultResetMonitorPrompt + "\n管理员补充规则：\n" + cfg.Prompt
	payload := map[string]any{"model": cfg.Model, "stream": false}
	endpoint := strings.TrimRight(baseURL, "/")
	if cfg.APIFormat == "responses" {
		endpoint += "/responses"
		payload["instructions"], payload["input"], payload["max_output_tokens"], payload["store"] = prompt, string(data), cfg.MaxOutputTokens, false
		payload["text"] = map[string]any{"format": map[string]string{"type": "json_object"}}
	} else {
		endpoint += "/chat/completions"
		payload["messages"] = []map[string]string{{"role": "system", "content": prompt}, {"role": "user", "content": string(data)}}
		payload["max_completion_tokens"] = cfg.MaxOutputTokens
		payload["response_format"] = map[string]string{"type": "json_object"}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("站内号池模型请求失败，请检查 Key、分组及模型可用性")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("站内号池模型返回 HTTP %d，请检查 Key 额度、分组及模型", response.StatusCode)
	}
	var result struct {
		Status  string `json:"status"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Output []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 256*1024)).Decode(&result); err != nil {
		return nil, fmt.Errorf("站内号池模型响应格式无效")
	}
	var output strings.Builder
	if cfg.APIFormat == "responses" {
		if result.Status != "completed" {
			return nil, fmt.Errorf("模型未完成输出，请检查输出长度和超时设置")
		}
		for _, item := range result.Output {
			for _, content := range item.Content {
				if content.Type == "output_text" {
					_, _ = output.WriteString(content.Text)
				}
			}
		}
	} else if len(result.Choices) == 1 && result.Choices[0].FinishReason == "stop" {
		_, _ = output.WriteString(result.Choices[0].Message.Content)
	}
	return parseResetMonitorAnalysis(output.String(), record)
}

func parseResetMonitorAnalysis(output string, record resetMonitorMessageRecord) (*resetMonitorAnalysis, error) {
	var analysis resetMonitorAnalysis
	if err := json.Unmarshal([]byte(output), &analysis); err != nil || !(analysis.Confidence >= 0 && analysis.Confidence <= 1) {
		return nil, fmt.Errorf("消息分析 JSON 或置信度无效")
	}
	switch analysis.Kind {
	case "direct_reset", "banked_reset", "forecast", "unknown":
	default:
		return nil, fmt.Errorf("消息分析返回未知事件类型")
	}
	if analysis.Kind == "direct_reset" || analysis.Kind == "banked_reset" {
		if utf8.RuneCountInString(analysis.Evidence) < 8 || utf8.RuneCountInString(analysis.Evidence) > 600 || (!strings.Contains(record.Subject, analysis.Evidence) && !strings.Contains(record.Body, analysis.Evidence)) {
			return nil, fmt.Errorf("重置依据必须逐字引用消息中的 8～600 个字符")
		}
	}
	analysis.Evidence = resetMonitorTruncate(analysis.Evidence, 600)
	return &analysis, nil
}
