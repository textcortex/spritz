package v1

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// BindingRuntimeNameForSequence returns the deterministic runtime name for one
// binding-owned candidate sequence.
func BindingRuntimeNameForSequence(
	bindingKey string,
	namePrefix string,
	presetID string,
	sequence int64,
) string {
	prefix := bindingRuntimePrefix(bindingKey, namePrefix, presetID)
	base := fmt.Sprintf("%s-%02d", prefix, sequence)
	if len(base) <= 63 {
		return base
	}
	return base[:63]
}

func bindingRuntimePrefix(bindingKey string, namePrefix string, presetID string) string {
	prefix := sanitizeBindingNameToken(namePrefix)
	if prefix == "" {
		prefix = sanitizeBindingNameToken(presetID)
	}
	if prefix == "" {
		prefix = "spritz"
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(bindingKey)))
	base := fmt.Sprintf("%s-%x", prefix, sum[:6])
	if len(base) <= 56 {
		return base
	}
	return base[:56]
}

func sanitizeBindingNameToken(value string) string {
	raw := strings.ToLower(strings.TrimSpace(value))
	if raw == "" {
		return ""
	}
	var out strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			out.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			out.WriteRune(r)
			lastDash = false
		default:
			if out.Len() == 0 || lastDash {
				continue
			}
			out.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(out.String(), "-")
}
