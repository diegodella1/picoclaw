package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type GmailTool struct {
	opt option.ClientOption
}

func NewGmailTool(saFile, email string) *GmailTool {
	opt, err := googleClientOption(saFile, email,
		gmail.GmailModifyScope,
	)
	if err != nil {
		return nil
	}
	return &GmailTool{opt: opt}
}

func (t *GmailTool) Name() string { return "gmail" }

func (t *GmailTool) Description() string {
	return "Gmail: search, read, send and reply to emails. Actions: search, read, send, reply, list_labels."
}

func (t *GmailTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform: search, read, send, reply, list_labels",
				"enum":        []string{"search", "read", "send", "reply", "list_labels"},
			},
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Gmail search query (for search action)",
			},
			"max_results": map[string]interface{}{
				"type":        "number",
				"description": "Maximum results to return (default 10)",
			},
			"id": map[string]interface{}{
				"type":        "string",
				"description": "Message ID (for read/reply actions)",
			},
			"to": map[string]interface{}{
				"type":        "string",
				"description": "Recipient email (for send action)",
			},
			"cc": map[string]interface{}{
				"type":        "string",
				"description": "CC email addresses, comma-separated (for send action)",
			},
			"subject": map[string]interface{}{
				"type":        "string",
				"description": "Email subject (for send action)",
			},
			"body": map[string]interface{}{
				"type":        "string",
				"description": "Email body (for send/reply actions)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *GmailTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	action, _ := args["action"].(string)

	srv, err := gmail.NewService(ctx, t.opt)
	if err != nil {
		return ErrorResult(fmt.Sprintf("gmail service error: %v", err))
	}

	switch action {
	case "search":
		return t.search(srv, args)
	case "read":
		return t.read(srv, args)
	case "send":
		return t.send(srv, args)
	case "reply":
		return t.reply(srv, args)
	case "list_labels":
		return t.listLabels(srv)
	default:
		return ErrorResult(fmt.Sprintf("unknown gmail action: %s", action))
	}
}

func (t *GmailTool) search(srv *gmail.Service, args map[string]interface{}) *ToolResult {
	query, _ := args["query"].(string)
	if query == "" {
		return ErrorResult("query is required for search")
	}

	maxResults := int64(10)
	if n, ok := args["max_results"].(float64); ok && n > 0 {
		maxResults = int64(n)
	}

	list, err := srv.Users.Messages.List("me").Q(query).MaxResults(maxResults).Do()
	if err != nil {
		return ErrorResult(fmt.Sprintf("search failed: %v", err))
	}

	if len(list.Messages) == 0 {
		return SilentResult("No messages found.")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d messages:\n\n", len(list.Messages)))

	for i, m := range list.Messages {
		msg, err := srv.Users.Messages.Get("me", m.Id).Format("metadata").
			MetadataHeaders("From", "Subject", "Date").Do()
		if err != nil {
			continue
		}

		from, subject, date := "", "", ""
		for _, h := range msg.Payload.Headers {
			switch h.Name {
			case "From":
				from = h.Value
			case "Subject":
				subject = h.Value
			case "Date":
				date = h.Value
			}
		}

		sb.WriteString(fmt.Sprintf("%d. ID: %s\n   From: %s\n   Subject: %s\n   Date: %s\n\n",
			i+1, m.Id, from, subject, date))
	}

	return SilentResult(sb.String())
}

func (t *GmailTool) read(srv *gmail.Service, args map[string]interface{}) *ToolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return ErrorResult("id is required for read")
	}

	msg, err := srv.Users.Messages.Get("me", id).Format("full").Do()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read message: %v", err))
	}

	var sb strings.Builder

	// Headers
	for _, h := range msg.Payload.Headers {
		switch h.Name {
		case "From", "To", "Cc", "Subject", "Date":
			sb.WriteString(fmt.Sprintf("%s: %s\n", h.Name, h.Value))
		}
	}
	sb.WriteString("\n")

	// Body
	body := extractBody(msg.Payload)
	sb.WriteString(body)

	return SilentResult(sb.String())
}

func extractBody(payload *gmail.MessagePart) string {
	// Try direct body
	if payload.MimeType == "text/plain" && payload.Body != nil && payload.Body.Data != "" {
		decoded, err := base64.URLEncoding.DecodeString(payload.Body.Data)
		if err == nil {
			return string(decoded)
		}
	}

	// Search parts recursively
	for _, part := range payload.Parts {
		if part.MimeType == "text/plain" && part.Body != nil && part.Body.Data != "" {
			decoded, err := base64.URLEncoding.DecodeString(part.Body.Data)
			if err == nil {
				return string(decoded)
			}
		}
		if len(part.Parts) > 0 {
			if body := extractBody(part); body != "" {
				return body
			}
		}
	}

	// Fallback to HTML
	for _, part := range payload.Parts {
		if part.MimeType == "text/html" && part.Body != nil && part.Body.Data != "" {
			decoded, err := base64.URLEncoding.DecodeString(part.Body.Data)
			if err == nil {
				return "[HTML content]\n" + string(decoded)
			}
		}
	}

	return "(no readable body)"
}

func (t *GmailTool) send(srv *gmail.Service, args map[string]interface{}) *ToolResult {
	to, _ := args["to"].(string)
	subject, _ := args["subject"].(string)
	body, _ := args["body"].(string)
	cc, _ := args["cc"].(string)

	if to == "" || subject == "" || body == "" {
		return ErrorResult("to, subject, and body are required for send")
	}

	var raw strings.Builder
	raw.WriteString(fmt.Sprintf("To: %s\r\n", to))
	if cc != "" {
		raw.WriteString(fmt.Sprintf("Cc: %s\r\n", cc))
	}
	raw.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	raw.WriteString("MIME-Version: 1.0\r\n")
	raw.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n\r\n")
	raw.WriteString(body)

	msg := &gmail.Message{
		Raw: base64.URLEncoding.EncodeToString([]byte(raw.String())),
	}

	sent, err := srv.Users.Messages.Send("me", msg).Do()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to send email: %v", err))
	}

	return SilentResult(fmt.Sprintf("Email sent successfully. Message ID: %s", sent.Id))
}

func (t *GmailTool) reply(srv *gmail.Service, args map[string]interface{}) *ToolResult {
	id, _ := args["id"].(string)
	body, _ := args["body"].(string)

	if id == "" || body == "" {
		return ErrorResult("id and body are required for reply")
	}

	// Get original message for headers
	orig, err := srv.Users.Messages.Get("me", id).Format("metadata").
		MetadataHeaders("From", "Subject", "Message-Id", "References", "In-Reply-To").Do()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get original message: %v", err))
	}

	var from, subject, messageID, references string
	for _, h := range orig.Payload.Headers {
		switch h.Name {
		case "From":
			from = h.Value
		case "Subject":
			subject = h.Value
		case "Message-Id":
			messageID = h.Value
		case "References":
			references = h.Value
		}
	}

	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}

	// Build references chain
	refs := references
	if refs != "" {
		refs += " " + messageID
	} else {
		refs = messageID
	}

	var raw strings.Builder
	raw.WriteString(fmt.Sprintf("To: %s\r\n", from))
	raw.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	raw.WriteString(fmt.Sprintf("In-Reply-To: %s\r\n", messageID))
	raw.WriteString(fmt.Sprintf("References: %s\r\n", refs))
	raw.WriteString("MIME-Version: 1.0\r\n")
	raw.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n\r\n")
	raw.WriteString(body)

	msg := &gmail.Message{
		Raw:      base64.URLEncoding.EncodeToString([]byte(raw.String())),
		ThreadId: orig.ThreadId,
	}

	sent, err := srv.Users.Messages.Send("me", msg).Do()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to reply: %v", err))
	}

	return SilentResult(fmt.Sprintf("Reply sent successfully. Message ID: %s", sent.Id))
}

func (t *GmailTool) listLabels(srv *gmail.Service) *ToolResult {
	labels, err := srv.Users.Labels.List("me").Do()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to list labels: %v", err))
	}

	var sb strings.Builder
	sb.WriteString("Labels:\n")
	for _, l := range labels.Labels {
		sb.WriteString(fmt.Sprintf("- %s (ID: %s)\n", l.Name, l.Id))
	}

	return SilentResult(sb.String())
}
