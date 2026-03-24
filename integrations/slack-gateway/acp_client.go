package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
)

type acpRPCMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data,omitempty"`
	} `json:"error,omitempty"`
}

func (g *slackGateway) promptConversation(ctx context.Context, serviceToken, namespace, conversationID, sessionID, cwd, prompt string) (string, bool, error) {
	wsURL, err := g.spritzWebSocketURL("/api/acp/conversations/"+url.PathEscape(conversationID)+"/connect", map[string]string{"namespace": namespace})
	if err != nil {
		return "", false, err
	}
	dialer := websocket.Dialer{HandshakeTimeout: g.cfg.HTTPTimeout}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+serviceToken)
	conn, _, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return "", false, err
	}
	defer conn.Close()

	client := &acpPromptClient{conn: conn}
	if _, _, err := client.call(ctx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
		"clientInfo": map[string]any{
			"name":    "slack-gateway",
			"title":   "Slack Gateway",
			"version": "1.0.0",
		},
	}, nil); err != nil {
		return "", false, err
	}
	if _, _, err := client.call(ctx, "session/load", map[string]any{
		"sessionId":  sessionID,
		"cwd":        cwd,
		"mcpServers": []any{},
	}, nil); err != nil {
		return "", false, err
	}
	var reply strings.Builder
	if _, promptSent, err := client.call(ctx, "session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt": []map[string]any{{
			"type": "text",
			"text": prompt,
		}},
	}, func(message *acpRPCMessage) {
		if strings.TrimSpace(message.Method) != "session/update" || len(message.Params) == 0 {
			return
		}
		var payload struct {
			Update map[string]any `json:"update"`
		}
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return
		}
		if strings.TrimSpace(stringValue(payload.Update["sessionUpdate"])) != "agent_message_chunk" {
			return
		}
		reply.WriteString(extractACPText(payload.Update["content"]))
	}); err != nil {
		return strings.TrimSpace(reply.String()), promptSent, err
	}
	text := strings.TrimSpace(reply.String())
	if text == "" {
		return "", true, fmt.Errorf("agent returned an empty reply")
	}
	return text, true, nil
}

type acpPromptClient struct {
	conn   *websocket.Conn
	nextID int64
}

func (c *acpPromptClient) writeJSON(ctx context.Context, payload any) error {
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetWriteDeadline(deadline)
	}
	return c.conn.WriteJSON(payload)
}

func (c *acpPromptClient) respondError(ctx context.Context, id any, code int, message string) error {
	return c.writeJSON(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func (c *acpPromptClient) call(ctx context.Context, method string, params any, onNotification func(*acpRPCMessage)) (json.RawMessage, bool, error) {
	c.nextID++
	requestID := fmt.Sprintf("rpc-%d", c.nextID)
	delivered := false
	if err := c.writeJSON(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      requestID,
		"method":  method,
		"params":  params,
	}); err != nil {
		return nil, false, err
	}
	for {
		if deadline, ok := ctx.Deadline(); ok {
			_ = c.conn.SetReadDeadline(deadline)
		}
		_, payload, err := c.conn.ReadMessage()
		if err != nil {
			return nil, delivered, err
		}
		var message acpRPCMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			return nil, delivered, err
		}
		if message.Method == "session/request_permission" && message.ID != nil {
			delivered = true
			if err := c.respondError(
				ctx,
				message.ID,
				-32000,
				"Permission denied: interactive approvals are unavailable in the Slack gateway.",
			); err != nil {
				return nil, delivered, err
			}
			continue
		}
		if message.Method != "" && message.ID == nil {
			delivered = true
			if onNotification != nil {
				onNotification(&message)
			}
			continue
		}
		if fmt.Sprint(message.ID) != requestID {
			continue
		}
		delivered = true
		if message.Error != nil {
			return nil, delivered, fmt.Errorf("%s", strings.TrimSpace(message.Error.Message))
		}
		return message.Result, delivered, nil
	}
}

func extractACPText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := extractACPText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		if text := stringValue(typed["text"]); text != "" {
			return text
		}
		if content, ok := typed["content"]; ok {
			return extractACPText(content)
		}
		if resource, ok := typed["resource"]; ok {
			return extractACPText(resource)
		}
		if uri := stringValue(typed["uri"]); uri != "" {
			return uri
		}
	}
	return ""
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}
