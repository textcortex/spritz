package main

import (
	"net/url"
	"path"
	"strconv"
	"strings"

	spritzv1 "spritz.sh/operator/api/v1"
)

// normalizeConversationCWD trims client input and preserves empty values so the
// conversation resource can distinguish "no override" from an explicit cwd.
func normalizeConversationCWD(value string) string {
	return strings.TrimSpace(value)
}

// resolveConversationEffectiveCWD resolves the cwd that should be used for ACP
// bootstrap and reconnect flows after accounting for explicit overrides,
// instance defaults, and legacy copied-default values.
func resolveConversationEffectiveCWD(spritz *spritzv1.Spritz, conversation *spritzv1.SpritzConversation) string {
	defaultCWD := resolveSpritzDefaultCWD(spritz)
	if conversation == nil {
		return defaultCWD
	}
	if override := normalizeConversationOverrideCWD(spritz, conversation); override != "" {
		return override
	}
	return defaultCWD
}

// normalizeConversationOverrideCWD distinguishes an explicit override from an
// inherited instance default without guessing about ambiguous historical values.
func normalizeConversationOverrideCWD(spritz *spritzv1.Spritz, conversation *spritzv1.SpritzConversation) string {
	if conversation == nil {
		return ""
	}
	override := normalizeConversationCWD(conversation.Spec.CWD)
	if override == "" {
		return ""
	}

	defaultCWD := resolveSpritzDefaultCWD(spritz)
	if conversationHasExplicitCWDOverride(conversation) {
		return override
	}
	if override == defaultCWD {
		return ""
	}
	return override
}

// resolveSpritzDefaultCWD derives the runtime-owned default cwd from explicit
// env overrides first and falls back to the primary repo checkout directory.
func resolveSpritzDefaultCWD(spritz *spritzv1.Spritz) string {
	if spritz == nil {
		return defaultACPCWD
	}

	for _, key := range []string{
		"SPRITZ_CONVERSATION_DEFAULT_CWD",
		"SPRITZ_CODEX_WORKDIR",
		"SPRITZ_CLAUDE_CODE_WORKDIR",
		"SPRITZ_REPO_DIR",
	} {
		if value := spritzEnvValue(spritz, key); value != "" {
			return value
		}
	}

	if repoDir := resolvePrimaryRepoDir(spritz); repoDir != "" {
		return repoDir
	}
	return defaultACPCWD
}

func spritzEnvValue(spritz *spritzv1.Spritz, key string) string {
	if spritz == nil {
		return ""
	}
	for i := len(spritz.Spec.Env) - 1; i >= 0; i-- {
		env := spritz.Spec.Env[i]
		if strings.TrimSpace(env.Name) != key {
			continue
		}
		if value := strings.TrimSpace(env.Value); value != "" {
			return value
		}
	}
	return ""
}

func resolvePrimaryRepoDir(spritz *spritzv1.Spritz) string {
	if spritz == nil {
		return ""
	}

	repos := spritz.Spec.Repos
	if len(repos) > 0 {
		return repoDirForConversationDefault(repos[0], 0, len(repos))
	}
	if spritz.Spec.Repo != nil && strings.TrimSpace(spritz.Spec.Repo.URL) != "" {
		return repoDirForConversationDefault(*spritz.Spec.Repo, 0, 1)
	}
	return ""
}

func repoDirForConversationDefault(repo spritzv1.SpritzRepo, index int, total int) string {
	repoDir := strings.TrimSpace(repo.Dir)
	if repoDir == "" {
		if total > 1 {
			repoDir = "/workspace/repo-" + strconv.Itoa(index+1)
		} else if inferred := inferConversationRepoName(repo.URL); inferred != "" {
			repoDir = path.Join("/workspace", inferred)
		} else {
			repoDir = "/workspace/repo"
		}
	}
	if !strings.HasPrefix(repoDir, "/") {
		repoDir = path.Join("/workspace", repoDir)
	}
	return path.Clean(repoDir)
}

func inferConversationRepoName(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	pathPart := ""
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return ""
		}
		pathPart = parsed.Path
	} else if strings.Contains(value, ":") {
		parts := strings.SplitN(value, ":", 2)
		if len(parts) == 2 {
			pathPart = parts[1]
		} else {
			pathPart = value
		}
	} else {
		pathPart = value
	}
	pathPart = strings.SplitN(pathPart, "?", 2)[0]
	pathPart = strings.SplitN(pathPart, "#", 2)[0]
	pathPart = strings.TrimSuffix(pathPart, "/")
	if pathPart == "" {
		return ""
	}
	base := path.Base(pathPart)
	if base == "." || base == "/" {
		return ""
	}
	base = strings.TrimSuffix(base, ".git")
	if base == "" || base == "." || base == "/" {
		return ""
	}
	return base
}

func conversationHasExplicitCWDOverride(conversation *spritzv1.SpritzConversation) bool {
	if conversation == nil || conversation.Annotations == nil {
		return false
	}
	value := strings.TrimSpace(conversation.Annotations[acpConversationExplicitCWDKey])
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func setConversationCWDOverride(conversation *spritzv1.SpritzConversation, value string) {
	if conversation == nil {
		return
	}
	conversation.Spec.CWD = normalizeConversationCWD(value)
	if conversation.Spec.CWD == "" {
		if conversation.Annotations != nil {
			delete(conversation.Annotations, acpConversationExplicitCWDKey)
		}
		return
	}
	if conversation.Annotations == nil {
		conversation.Annotations = map[string]string{}
	}
	conversation.Annotations[acpConversationExplicitCWDKey] = "true"
}
