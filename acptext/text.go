package acptext

import (
	"fmt"
	"strings"
)

// Extract returns the readable text content represented by one ACP payload
// without trimming or normalizing whitespace.
func Extract(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			text := Extract(item)
			if text == "" {
				continue
			}
			parts = append(parts, text)
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		if text, ok := typed["text"].(string); ok && text != "" {
			return text
		}
		if content, ok := typed["content"]; ok {
			return Extract(content)
		}
		if resource, ok := typed["resource"]; ok {
			return Extract(resource)
		}
		if uri, ok := typed["uri"].(string); ok {
			return uri
		}
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

// JoinChunks concatenates ACP chunk payloads without injecting separators or
// trimming whitespace at chunk boundaries.
func JoinChunks(values []any) string {
	var builder strings.Builder
	for _, value := range values {
		builder.WriteString(Extract(value))
	}
	return builder.String()
}
