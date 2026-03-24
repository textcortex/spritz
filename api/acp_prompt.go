package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type acpPromptResult struct {
	StopReason    string           `json:"stopReason,omitempty"`
	AssistantText string           `json:"assistantText,omitempty"`
	Updates       []map[string]any `json:"updates,omitempty"`
}

func (c *acpBootstrapInstanceClient) prompt(ctx context.Context, sessionID, text string, settleTimeout time.Duration) (*acpPromptResult, error) {
	c.nextID++
	requestID := fmt.Sprintf("prompt-%d", c.nextID)
	if err := c.writeJSON(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      requestID,
		"method":  "session/prompt",
		"params": map[string]any{
			"sessionId": sessionID,
			"prompt": []map[string]any{{
				"type": "text",
				"text": text,
			}},
		},
	}); err != nil {
		return nil, err
	}

	var result struct {
		StopReason string `json:"stopReason"`
	}
	updates := make([]map[string]any, 0, 8)

	for {
		message, err := c.readMessage(ctx)
		if err != nil {
			return nil, err
		}
		if update, ok := sessionUpdateFromMessage(message); ok {
			updates = append(updates, update)
			continue
		}
		if fmt.Sprint(message.ID) != requestID {
			continue
		}
		if message.Error != nil {
			return nil, newACPBootstrapRPCError(message.Error)
		}
		if len(message.Result) > 0 {
			if err := json.Unmarshal(message.Result, &result); err != nil {
				return nil, err
			}
		}
		break
	}

	if err := c.drainSessionUpdates(ctx, settleTimeout, &updates); err != nil {
		return nil, err
	}

	return &acpPromptResult{
		StopReason:    strings.TrimSpace(result.StopReason),
		AssistantText: assistantTextFromACPUpdates(updates),
		Updates:       updates,
	}, nil
}

func (c *acpBootstrapInstanceClient) drainSessionUpdates(ctx context.Context, settleTimeout time.Duration, updates *[]map[string]any) error {
	if c == nil || c.conn == nil || settleTimeout <= 0 {
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		deadline := time.Now().Add(settleTimeout)
		if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
			deadline = ctxDeadline
		}
		if err := c.conn.SetReadDeadline(deadline); err != nil {
			return err
		}
		_, payload, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				return nil
			}
			return err
		}
		message := &acpBootstrapJSONRPCMessage{}
		if err := json.Unmarshal(payload, message); err != nil {
			return err
		}
		if update, ok := sessionUpdateFromMessage(message); ok {
			*updates = append(*updates, update)
		}
	}
}

func sessionUpdateFromMessage(message *acpBootstrapJSONRPCMessage) (map[string]any, bool) {
	if message == nil || message.Method != "session/update" || len(message.Params) == 0 {
		return nil, false
	}
	var params struct {
		Update map[string]any `json:"update"`
	}
	if err := json.Unmarshal(message.Params, &params); err != nil {
		return nil, false
	}
	if len(params.Update) == 0 {
		return nil, false
	}
	return params.Update, true
}

func assistantTextFromACPUpdates(updates []map[string]any) string {
	chunks := make([]any, 0, len(updates))
	for _, update := range updates {
		if strings.TrimSpace(fmt.Sprint(update["sessionUpdate"])) != "agent_message_chunk" {
			continue
		}
		chunks = append(chunks, update["content"])
	}
	return joinACPTextChunks(chunks)
}

func joinACPTextChunks(values []any) string {
	var builder strings.Builder
	for _, value := range values {
		builder.WriteString(extractACPText(value))
	}
	return builder.String()
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
			text := extractACPText(item)
			if text == "" {
				continue
			}
			parts = append(parts, text)
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			return text
		}
		if content, ok := typed["content"]; ok {
			return extractACPText(content)
		}
		if resource, ok := typed["resource"].(map[string]any); ok {
			if text, ok := resource["text"].(string); ok {
				return text
			}
			if uri, ok := resource["uri"].(string); ok {
				return uri
			}
		}
		return ""
	default:
		return fmt.Sprint(typed)
	}
}
