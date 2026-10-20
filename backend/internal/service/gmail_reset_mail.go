package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

type gmailResetPart struct {
	MIMEType string `json:"mimeType"`
	Filename string `json:"filename"`
	Headers  []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"headers"`
	Body struct {
		Data string `json:"data"`
	} `json:"body"`
	Parts []gmailResetPart `json:"parts"`
}

type gmailResetMessage struct {
	ID           string         `json:"id"`
	InternalDate string         `json:"internalDate"`
	Payload      gmailResetPart `json:"payload"`
}

type gmailResetMailRecord struct {
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

type gmailResetAnalysis struct {
	Kind       string  `json:"kind"`
	Confidence float64 `json:"confidence"`
	Evidence   string  `json:"evidence"`
}

var gmailResetDMARC = regexp.MustCompile(`(?i)(?:^|[;\s])dmarc=pass(?:\s|;|$)`)

func gmailResetHeader(part gmailResetPart, name string) string {
	for _, header := range part.Headers {
		if strings.EqualFold(header.Name, name) {
			return header.Value
		}
	}
	return ""
}

func gmailResetSenderAllowed(part gmailResetPart, cfg GmailResetConfig) (string, bool) {
	address, err := mail.ParseAddress(gmailResetHeader(part, "From"))
	if err != nil {
		return "", false
	}
	sender := strings.ToLower(address.Address)
	allowed := false
	for _, candidate := range cfg.Senders {
		allowed = allowed || strings.EqualFold(sender, candidate)
	}
	if !allowed {
		return sender, false
	}
	if !cfg.RequireAuthenticatedSender {
		return sender, true
	}
	senderAddress, parseSenderErr := mail.ParseAddress(sender)
	if parseSenderErr != nil {
		return sender, false
	}
	senderDomain := strings.ToLower(strings.TrimSpace(strings.SplitN(senderAddress.Address, "@", 2)[1]))
	for _, header := range part.Headers {
		if strings.EqualFold(header.Name, "Authentication-Results") && strings.HasPrefix(strings.ToLower(strings.TrimSpace(header.Value)), "mx.google.com;") {
			value := strings.ToLower(header.Value)
			fromIndex := strings.Index(value, "header.from=")
			fromDomain := ""
			if fromIndex >= 0 {
				fields := strings.FieldsFunc(value[fromIndex+len("header.from="):], func(r rune) bool { return r == ';' || r == ' ' || r == '\t' })
				if len(fields) > 0 {
					fromDomain = fields[0]
				}
			}
			return sender, gmailResetDMARC.MatchString(header.Value) && fromDomain == senderDomain
		}
	}
	return sender, false
}

func gmailResetBody(part gmailResetPart, depth int) string {
	if depth > 20 || part.Filename != "" {
		return ""
	}
	if part.MIMEType == "text/plain" || part.MIMEType == "text/html" {
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(part.Body.Data, "="))
		if err != nil || !utf8.Valid(raw) {
			return ""
		}
		if part.MIMEType == "text/plain" {
			return strings.TrimSpace(string(raw))
		}
		document, err := html.Parse(bytes.NewReader(raw))
		if err != nil {
			return ""
		}
		var builder strings.Builder
		var walk func(*html.Node)
		walk = func(node *html.Node) {
			if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style" || node.Data == "head") {
				return
			}
			if node.Type == html.TextNode {
				builder.WriteString(node.Data)
				builder.WriteByte(' ')
			}
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
		}
		walk(document)
		return strings.Join(strings.Fields(builder.String()), " ")
	}
	var fallback string
	for _, child := range part.Parts {
		text := gmailResetBody(child, depth+1)
		if text == "" {
			continue
		}
		if child.MIMEType == "text/plain" || child.MIMEType == "multipart/alternative" {
			return text
		}
		if fallback == "" {
			fallback = text
		}
	}
	return fallback
}

func gmailResetTruncate(value string, limit int) string {
	characters := []rune(strings.ToValidUTF8(value, ""))
	if len(characters) > limit {
		characters = characters[:limit]
	}
	return string(characters)
}

func gmailResetQuery(stored *gmailResetStored, now time.Time) string {
	from := make([]string, 0, len(stored.Config.Senders))
	for _, sender := range stored.Config.Senders {
		from = append(from, "from:"+sender)
	}
	after := now.Add(-time.Duration(stored.Config.MaxAgeHours) * time.Hour)
	if stored.Since.After(after) {
		after = stored.Since
	}
	return fmt.Sprintf("{%s} after:%d %s", strings.Join(from, " "), after.Unix(), stored.Config.Query)
}

func gmailResetList(ctx context.Context, client *http.Client, token, query string) ([]string, error) {
	ids := make([]string, 0)
	pageToken := ""
	for page := 0; page < 10; page++ {
		params := url.Values{"q": {query}, "maxResults": {"100"}}
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}
		var result struct {
			Messages []struct {
				ID string `json:"id"`
			} `json:"messages"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := gmailResetGet(ctx, client, token, "https://gmail.googleapis.com/gmail/v1/users/me/messages?"+params.Encode(), &result); err != nil {
			return nil, err
		}
		for _, message := range result.Messages {
			if message.ID != "" {
				ids = append(ids, message.ID)
			}
		}
		pageToken = result.NextPageToken
		if pageToken == "" {
			return ids, nil
		}
	}
	return nil, fmt.Errorf("Gmail 待处理邮件超过 1000 封，请缩小发件人或查询条件")
}

func gmailResetAnalyze(ctx context.Context, client *http.Client, cfg GmailResetConfig, key string, record gmailResetMailRecord) (*gmailResetAnalysis, error) {
	data, err := json.Marshal(map[string]string{"sender": record.Sender, "received_at": record.ReceivedAt.UTC().Format(time.RFC3339), "subject": record.Subject, "body": record.Body})
	if err != nil {
		return nil, err
	}
	prompt := defaultGmailResetPrompt + "\n管理员补充规则：\n" + cfg.Prompt
	payload := map[string]any{"model": cfg.Model, "stream": false}
	endpoint := strings.TrimRight(cfg.ModelBaseURL, "/")
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("邮件分析模型地址无效")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("邮件分析模型请求失败，请检查网络和接口配置")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("邮件分析模型返回 HTTP %d", response.StatusCode)
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
		return nil, fmt.Errorf("邮件分析模型响应格式无效")
	}
	var output strings.Builder
	if cfg.APIFormat == "responses" {
		if result.Status != "completed" {
			return nil, fmt.Errorf("邮件分析模型未完成输出，请检查最大输出长度")
		}
		for _, item := range result.Output {
			for _, content := range item.Content {
				if content.Type == "output_text" {
					output.WriteString(content.Text)
				}
			}
		}
	} else if len(result.Choices) == 1 && result.Choices[0].FinishReason == "stop" {
		output.WriteString(result.Choices[0].Message.Content)
	}
	return parseGmailResetAnalysis(output.String(), record)
}

func parseGmailResetAnalysis(output string, record gmailResetMailRecord) (*gmailResetAnalysis, error) {
	var analysis gmailResetAnalysis
	if err := json.Unmarshal([]byte(output), &analysis); err != nil || !(analysis.Confidence >= 0 && analysis.Confidence <= 1) {
		return nil, fmt.Errorf("邮件分析 JSON 或置信度无效")
	}
	switch analysis.Kind {
	case "direct_reset", "banked_reset", "forecast", "unknown":
	default:
		return nil, fmt.Errorf("邮件分析返回未知事件类型")
	}
	if analysis.Kind == "direct_reset" || analysis.Kind == "banked_reset" {
		if utf8.RuneCountInString(analysis.Evidence) < 8 || utf8.RuneCountInString(analysis.Evidence) > 600 || (!strings.Contains(record.Subject, analysis.Evidence) && !strings.Contains(record.Body, analysis.Evidence)) {
			return nil, fmt.Errorf("重置依据必须逐字引用邮件主题或正文中的 8～600 个字符")
		}
	}
	analysis.Evidence = gmailResetTruncate(analysis.Evidence, 600)
	return &analysis, nil
}
