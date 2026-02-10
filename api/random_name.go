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

func createRandomSpritzName(isTaken func(string) bool) string {
	used := isTaken
	if used == nil {
		used = func(string) bool { return false }
	}

	for attempt := 0; attempt < 12; attempt++ {
		base := createSpritzNameBase(2)
		if !used(base) {
			return base
		}
		for i := 2; i <= 12; i++ {
			candidate := base + "-" + strconv.Itoa(i)
			if !used(candidate) {
				return candidate
			}
		}
	}

	for attempt := 0; attempt < 12; attempt++ {
		base := createSpritzNameBase(3)
		if !used(base) {
			return base
		}
		for i := 2; i <= 12; i++ {
			candidate := base + "-" + strconv.Itoa(i)
			if !used(candidate) {
				return candidate
			}
		}
	}

	fallback := createSpritzNameBase(3) + "-" + randomSuffix(3)
	if used(fallback) {
		return fallback + "-" + randomSuffix(4)
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

func (s *server) newSpritzNameGenerator(ctx context.Context, namespace string) (func() string, error) {
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
		name := createRandomSpritzName(func(candidate string) bool {
			_, ok := existing[candidate]
			return ok
		})
		existing[name] = struct{}{}
		return name
	}, nil
}
