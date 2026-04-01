package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"spritz.sh/acptext"
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
		if handled, err := c.handleServerRequest(ctx, message, "Method not supported by internal debug chat."); handled {
			if err != nil {
				return nil, err
			}
			continue
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
	c.startReader()
	timer := time.NewTimer(settleTimeout)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case err, ok := <-c.readErrCh:
			if !ok || err == nil {
				return nil
			}
			return err
		case message, ok := <-c.readCh:
			if !ok {
				return nil
			}
			if handled, err := c.handleServerRequest(ctx, message, "Method not supported by internal debug chat."); handled {
				if err != nil {
					return err
				}
				continue
			}
			if update, ok := sessionUpdateFromMessage(message); ok {
				*updates = append(*updates, update)
				if !shouldExtendSessionSettleWindow(update) {
					continue
				}
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(settleTimeout)
		}
	}
}

func shouldExtendSessionSettleWindow(update map[string]any) bool {
	switch strings.TrimSpace(fmt.Sprint(update["sessionUpdate"])) {
	case "", "heartbeat", "ping", "pong", "ack", "available_commands_update", "current_mode_update", "usage_update", "session_info_update":
		return false
	default:
		return true
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
	return acptext.JoinChunks(chunks)
}
