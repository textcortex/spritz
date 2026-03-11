package main

import (
	"context"
	"crypto/rand"
	"math/big"
	"strconv"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"
	spritzv1 "spritz.sh/operator/api/v1"
)

var spritzNameAdjectives = []string{
	"amber",
	"briny",
	"brisk",
	"calm",
	"clear",
	"cool",
	"crisp",
	"dawn",
	"delta",
	"ember",
	"faint",
	"fast",
	"fresh",
	"gentle",
	"glow",
	"good",
	"grand",
	"keen",
	"kind",
	"lucky",
	"marine",
	"mellow",
	"mild",
	"neat",
	"nimble",
	"nova",
	"oceanic",
	"plaid",
	"quick",
	"quiet",
	"rapid",
	"salty",
	"sharp",
	"swift",
	"tender",
	"tidal",
	"tidy",
	"tide",
	"vivid",
	"warm",
	"wild",
	"young",
}

var spritzNameNouns = []string{
	"atlas",
	"basil",
	"bison",
	"bloom",
	"breeze",
	"canyon",
	"cedar",
	"claw",
	"cloud",
	"comet",
	"coral",
	"cove",
	"crest",
	"crustacean",
	"daisy",
	"dune",
	"ember",
	"falcon",
	"fjord",
	"forest",
	"glade",
	"gulf",
	"harbor",
	"haven",
	"kelp",
	"lagoon",
	"lobster",
	"meadow",
	"mist",
	"nudibranch",
	"nexus",
	"ocean",
	"orbit",
	"otter",
	"pine",
	"prairie",
	"reef",
	"ridge",
	"river",
	"rook",
	"sable",
	"sage",
	"seaslug",
	"shell",
	"shoal",
	"shore",
	"slug",
	"summit",
	"tidepool",
	"trail",
	"valley",
	"wharf",
	"willow",
	"zephyr",
}

func randomChoice(values []string, fallback string) string {
	if len(values) == 0 {
		return fallback
	}
	idx := randomIndex(len(values))
	if idx < 0 || idx >= len(values) {
		return fallback
	}
	return values[idx]
}

func randomIndex(max int) int {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0
	}
	return int(n.Int64())
}

func sanitizeSpritzNameToken(value string) string {
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

func deriveSpritzNamePrefixFromImage(image string) string {
	raw := strings.TrimSpace(image)
	if raw == "" {
		return ""
	}
	if idx := strings.LastIndex(raw, "/"); idx >= 0 {
		raw = raw[idx+1:]
	}
	if idx := strings.Index(raw, "@"); idx >= 0 {
		raw = raw[:idx]
	}
	if idx := strings.Index(raw, ":"); idx >= 0 {
		raw = raw[:idx]
	}
	prefix := sanitizeSpritzNameToken(raw)
	if trimmed := strings.TrimPrefix(prefix, "spritz-"); trimmed != "" && trimmed != prefix {
		return trimmed
	}
	return prefix
}

func resolveSpritzNamePrefix(explicit, image string) string {
	if prefix := sanitizeSpritzNameToken(explicit); prefix != "" {
		return prefix
	}
	return deriveSpritzNamePrefixFromImage(image)
}

func createSpritzNameBase(words int) string {
	parts := []string{
		randomChoice(spritzNameAdjectives, "steady"),
		randomChoice(spritzNameNouns, "harbor"),
	}
	if words > 2 {
		parts = append(parts, randomChoice(spritzNameNouns, "reef"))
	}
	return strings.Join(parts, "-")
}

func joinSpritzName(prefix, base, suffix string) string {
	tailParts := []string{}
	if base != "" {
		tailParts = append(tailParts, sanitizeSpritzNameToken(base))
	}
	if suffix != "" {
		tailParts = append(tailParts, sanitizeSpritzNameToken(suffix))
	}
	tail := strings.Trim(strings.Join(tailParts, "-"), "-")
	if tail == "" {
		tail = "spritz"
	}
	prefix = sanitizeSpritzNameToken(prefix)
	if prefix == "" {
		if len(tail) <= 63 {
			return tail
		}
		return strings.Trim(tail[:63], "-")
	}
	if len(prefix)+1+len(tail) <= 63 {
		return prefix + "-" + tail
	}
	maxPrefixLen := 63 - 1 - len(tail)
	if maxPrefixLen <= 0 {
		if len(tail) <= 63 {
			return tail
		}
		return strings.Trim(tail[:63], "-")
	}
	if len(prefix) > maxPrefixLen {
		prefix = strings.Trim(prefix[:maxPrefixLen], "-")
	}
	if prefix == "" {
		if len(tail) <= 63 {
			return tail
		}
		return strings.Trim(tail[:63], "-")
	}
	return prefix + "-" + tail
}

func createRandomSpritzName(prefix string, isTaken func(string) bool) string {
	used := isTaken
	if used == nil {
		used = func(string) bool { return false }
	}

	for attempt := 0; attempt < 12; attempt++ {
		nameBase := createSpritzNameBase(2)
		base := joinSpritzName(prefix, nameBase, "")
		if !used(base) {
			return base
		}
		for i := 2; i <= 12; i++ {
			candidate := joinSpritzName(prefix, nameBase, strconv.Itoa(i))
			if !used(candidate) {
				return candidate
			}
		}
	}

	for attempt := 0; attempt < 12; attempt++ {
		nameBase := createSpritzNameBase(3)
		base := joinSpritzName(prefix, nameBase, "")
		if !used(base) {
			return base
		}
		for i := 2; i <= 12; i++ {
			candidate := joinSpritzName(prefix, nameBase, strconv.Itoa(i))
			if !used(candidate) {
				return candidate
			}
		}
	}

	nameBase := createSpritzNameBase(3)
	fallback := joinSpritzName(prefix, nameBase, randomSuffix(3))
	if used(fallback) {
		return joinSpritzName(prefix, nameBase, randomSuffix(4))
	}
	return fallback
}

func randomSuffix(length int) string {
	if length <= 0 {
		return "x"
	}
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	var out strings.Builder
	for i := 0; i < length; i++ {
		out.WriteByte(charset[randomIndex(len(charset))])
	}
	return out.String()
}

func (s *server) newSpritzNameGenerator(ctx context.Context, namespace string, prefix string) (func() string, error) {
	list := &spritzv1.SpritzList{}
	opts := []client.ListOption{client.InNamespace(namespace)}
	if err := s.client.List(ctx, list, opts...); err != nil {
		return nil, err
	}
	existing := map[string]struct{}{}
	for _, item := range list.Items {
		if item.Name != "" {
			existing[item.Name] = struct{}{}
		}
	}
	return func() string {
		name := createRandomSpritzName(prefix, func(candidate string) bool {
			_, ok := existing[candidate]
			return ok
		})
		existing[name] = struct{}{}
		return name
	}, nil
}
