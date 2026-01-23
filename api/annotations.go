package main

import (
	"fmt"
	"strings"
)

func parseKeyValueCSV(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	out := map[string]string{}
	parts := strings.Split(raw, ",")
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		kv := strings.SplitN(trimmed, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid key/value entry: %s", trimmed)
		}
		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])
		if key == "" || value == "" {
			return nil, fmt.Errorf("invalid key/value entry: %s", trimmed)
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func mergeStringMap(base, overrides map[string]string) map[string]string {
	if len(base) == 0 && len(overrides) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overrides {
		if k == "" || v == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
