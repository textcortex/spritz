package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/labstack/echo/v4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	spritzv1 "spritz.sh/operator/api/v1"
)

var errACPUnavailable = errors.New("acp unavailable")

func spritzSupportsACPConversations(spritz *spritzv1.Spritz) bool {
	return spritz != nil &&
		spritz.Status.Phase == "Ready" &&
		spritz.Status.ACP != nil &&
		spritz.Status.ACP.State == "ready" &&
		spritz.Status.ACP.Capabilities != nil &&
		spritz.Status.ACP.Capabilities.LoadSession
}

func displayAgentName(spritz *spritzv1.Spritz) string {
	if spritz == nil {
		return ""
	}
	if profile := currentSpritzStatusProfile(spritz); profile != nil && strings.TrimSpace(profile.Name) != "" {
		return strings.TrimSpace(profile.Name)
	}
	if profile := normalizeSpritzAgentProfile(spritz.Spec.ProfileOverrides); profile != nil && strings.TrimSpace(profile.Name) != "" {
		return strings.TrimSpace(profile.Name)
	}
	if spritz.Status.ACP == nil || spritz.Status.ACP.AgentInfo == nil {
		return spritz.Name
	}
	info := spritz.Status.ACP.AgentInfo
	if strings.TrimSpace(info.Title) != "" {
		return strings.TrimSpace(info.Title)
	}
	if strings.TrimSpace(info.Name) != "" {
		return strings.TrimSpace(info.Name)
	}
	return spritz.Name
}

func normalizeConversationAgentInfo(status *spritzv1.SpritzACPStatus) *spritzv1.SpritzACPAgentInfo {
	if status == nil || status.AgentInfo == nil {
		return nil
	}
	return &spritzv1.SpritzACPAgentInfo{
		Name:    status.AgentInfo.Name,
		Title:   status.AgentInfo.Title,
		Version: status.AgentInfo.Version,
	}
}

func normalizeConversationCapabilities(status *spritzv1.SpritzACPStatus) *spritzv1.SpritzACPCapabilities {
	if status == nil || status.Capabilities == nil {
		return nil
	}
	capabilities := &spritzv1.SpritzACPCapabilities{}
	status.Capabilities.DeepCopyInto(capabilities)
	return capabilities
}

func normalizeConversationCWD(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return defaultACPCWD
	}
	return trimmed
}

func conversationDisplayTitle(conversation *spritzv1.SpritzConversation) string {
	if conversation == nil {
		return defaultACPConversationTitle
	}
	if strings.TrimSpace(conversation.Spec.Title) != "" {
		return strings.TrimSpace(conversation.Spec.Title)
	}
	return defaultACPConversationTitle
}

func sortACPConversations(items []spritzv1.SpritzConversation) {
	sort.Slice(items, func(i, j int) bool {
		left := items[i].CreationTimestamp.Time
		right := items[j].CreationTimestamp.Time
		if left.Equal(right) {
			return items[i].Name > items[j].Name
		}
		return left.After(right)
	})
}

func newConversationName(spritzName string) (string, error) {
	prefix := strings.ToLower(strings.TrimSpace(spritzName))
	prefix = strings.Trim(prefix, "-")
	if prefix == "" {
		prefix = "conversation"
	}
	if len(prefix) > 52 {
		prefix = prefix[:52]
		prefix = strings.TrimRight(prefix, "-")
		if prefix == "" {
			prefix = "conversation"
		}
	}
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(suffix)), nil
}

func buildACPConversationResource(spritz *spritzv1.Spritz, requestedTitle, requestedCWD string) (*spritzv1.SpritzConversation, error) {
	name, err := newConversationName(spritz.Name)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(requestedTitle)
	if title == "" {
		title = defaultACPConversationTitle
	}
	return &spritzv1.SpritzConversation{
		TypeMeta: metav1.TypeMeta{
			APIVersion: spritzv1.GroupVersion.String(),
			Kind:       "SpritzConversation",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: spritz.Namespace,
			Labels: map[string]string{
				acpConversationOwnerLabelKey:  ownerLabelValue(spritz.Spec.Owner.ID),
				acpConversationSpritzLabelKey: spritz.Name,
				acpConversationLabelKey:       acpConversationLabelValue,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: spritzv1.GroupVersion.String(),
				Kind:       "Spritz",
				Name:       spritz.Name,
				UID:        spritz.UID,
			}},
		},
		Spec: spritzv1.SpritzConversationSpec{
			SpritzName:   spritz.Name,
			Owner:        spritz.Spec.Owner,
			Title:        title,
			CWD:          normalizeConversationCWD(requestedCWD),
			AgentInfo:    normalizeConversationAgentInfo(spritz.Status.ACP),
			Capabilities: normalizeConversationCapabilities(spritz.Status.ACP),
		},
		Status: spritzv1.SpritzConversationStatus{
			BindingState: "pending",
		},
	}, nil
}

func (s *server) getAuthorizedSpritz(ctx context.Context, principal principal, namespace, name string) (*spritzv1.Spritz, error) {
	spritz := &spritzv1.Spritz{}
	if err := s.client.Get(ctx, clientKey(namespace, name), spritz); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, err
	}
	if err := authorizeHumanOwnedAccess(principal, spritz.Spec.Owner.ID, s.auth.enabled()); err != nil {
		return nil, errForbidden
	}
	return spritz, nil
}

func (s *server) getAuthorizedACPReadySpritz(ctx context.Context, principal principal, namespace, name string) (*spritzv1.Spritz, error) {
	spritz, err := s.getAuthorizedSpritz(ctx, principal, namespace, name)
	if err != nil {
		return nil, err
	}
	if !spritzSupportsACPConversations(spritz) {
		return nil, errACPUnavailable
	}
	return spritz, nil
}

func (s *server) getAuthorizedACPReadySpritzForConversation(
	ctx context.Context,
	conversation *spritzv1.SpritzConversation,
	namespace string,
) (*spritzv1.Spritz, error) {
	spritz := &spritzv1.Spritz{}
	if err := s.client.Get(ctx, clientKey(namespace, conversation.Spec.SpritzName), spritz); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, err
		}
		return nil, err
	}
	if strings.TrimSpace(spritz.Spec.Owner.ID) != strings.TrimSpace(conversation.Spec.Owner.ID) {
		return nil, errForbidden
	}
	if !spritzSupportsACPConversations(spritz) {
		return nil, errACPUnavailable
	}
	return spritz, nil
}

func (s *server) requestNamespace(c echo.Context) string {
	namespace := s.namespace
	if namespace == "" {
		namespace = strings.TrimSpace(c.QueryParam("namespace"))
	}
	return namespace
}

func (s *server) acpInstanceURL(namespace, name string) string {
	if s.acp.instanceURL != nil {
		return s.acp.instanceURL(namespace, name)
	}
	return (&url.URL{
		Scheme: "ws",
		Host:   fmt.Sprintf("%s.%s.svc.cluster.local:%d", name, namespace, s.acp.port),
		Path:   s.acp.path,
	}).String()
}

func (s *server) writeACPResourceError(c echo.Context, err error) error {
	switch {
	case apierrors.IsNotFound(err):
		return writeError(c, http.StatusNotFound, "spritz not found")
	case errors.Is(err, errForbidden):
		return writeError(c, http.StatusForbidden, "forbidden")
	case errors.Is(err, errACPUnavailable):
		return writeError(c, http.StatusConflict, "acp unavailable")
	default:
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
}
