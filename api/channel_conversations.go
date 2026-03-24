package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

const (
	channelConversationRouteLabelKey                       = "spritz.sh/channel-route"
	channelConversationPrincipalAnnotationKey              = "spritz.sh/channel-principal-id"
	channelConversationProviderAnnotationKey               = "spritz.sh/channel-provider"
	channelConversationExternalScopeTypeAnnotationKey      = "spritz.sh/channel-external-scope-type"
	channelConversationExternalTenantIDAnnotationKey       = "spritz.sh/channel-external-tenant-id"
	channelConversationExternalChannelIDAnnotationKey      = "spritz.sh/channel-external-channel-id"
	channelConversationExternalConversationIDAnnotationKey = "spritz.sh/channel-external-conversation-id"
)

type channelConversationUpsertRequest struct {
	RequestID              string `json:"requestId,omitempty"`
	Namespace              string `json:"namespace,omitempty"`
	InstanceID             string `json:"instanceId"`
	OwnerID                string `json:"ownerId"`
	Provider               string `json:"provider"`
	ExternalScopeType      string `json:"externalScopeType"`
	ExternalTenantID       string `json:"externalTenantId"`
	ExternalChannelID      string `json:"externalChannelId"`
	ExternalConversationID string `json:"externalConversationId"`
	Title                  string `json:"title,omitempty"`
	CWD                    string `json:"cwd,omitempty"`
}

type normalizedChannelConversationIdentity struct {
	principalID            string
	provider               string
	externalScopeType      string
	externalTenantID       string
	externalChannelID      string
	externalConversationID string
}

// normalizeChannelConversationUpsertRequest validates the route/thread identity
// used to upsert a persistent ACP conversation mapping.
func normalizeChannelConversationUpsertRequest(principalID string, body channelConversationUpsertRequest) (channelConversationUpsertRequest, normalizedChannelConversationIdentity, error) {
	body.RequestID = strings.TrimSpace(body.RequestID)
	body.Namespace = strings.TrimSpace(body.Namespace)
	body.InstanceID = sanitizeSpritzNameToken(body.InstanceID)
	body.OwnerID = strings.TrimSpace(body.OwnerID)
	body.Title = strings.TrimSpace(body.Title)
	body.CWD = normalizeConversationCWD(body.CWD)

	route, err := normalizeChannelRouteResolveRequest(channelRouteResolveRequest{
		RequestID:         body.RequestID,
		Provider:          body.Provider,
		ExternalScopeType: body.ExternalScopeType,
		ExternalTenantID:  body.ExternalTenantID,
	})
	if err != nil {
		return channelConversationUpsertRequest{}, normalizedChannelConversationIdentity{}, err
	}
	body.Provider = route.Provider
	body.ExternalScopeType = route.ExternalScopeType
	body.ExternalTenantID = route.ExternalTenantID

	if body.InstanceID == "" {
		return channelConversationUpsertRequest{}, normalizedChannelConversationIdentity{}, echo.NewHTTPError(http.StatusBadRequest, "instanceId is required")
	}
	if body.OwnerID == "" {
		return channelConversationUpsertRequest{}, normalizedChannelConversationIdentity{}, echo.NewHTTPError(http.StatusBadRequest, "ownerId is required")
	}
	body.ExternalChannelID = strings.TrimSpace(body.ExternalChannelID)
	if body.ExternalChannelID == "" {
		return channelConversationUpsertRequest{}, normalizedChannelConversationIdentity{}, echo.NewHTTPError(http.StatusBadRequest, "externalChannelId is required")
	}
	body.ExternalConversationID = strings.TrimSpace(body.ExternalConversationID)
	if body.ExternalConversationID == "" {
		return channelConversationUpsertRequest{}, normalizedChannelConversationIdentity{}, echo.NewHTTPError(http.StatusBadRequest, "externalConversationId is required")
	}

	return body, normalizedChannelConversationIdentity{
		principalID:            strings.TrimSpace(principalID),
		provider:               body.Provider,
		externalScopeType:      body.ExternalScopeType,
		externalTenantID:       body.ExternalTenantID,
		externalChannelID:      body.ExternalChannelID,
		externalConversationID: body.ExternalConversationID,
	}, nil
}

func channelConversationRouteHash(identity normalizedChannelConversationIdentity, instanceID string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		identity.principalID,
		identity.provider,
		identity.externalScopeType,
		identity.externalTenantID,
		identity.externalChannelID,
		identity.externalConversationID,
		strings.TrimSpace(instanceID),
	}, "\n")))
	return hex.EncodeToString(sum[:16])
}

func channelConversationName(spritzName string, identity normalizedChannelConversationIdentity) string {
	prefix := strings.ToLower(strings.TrimSpace(spritzName))
	prefix = strings.Trim(prefix, "-")
	if prefix == "" {
		prefix = "conversation"
	}
	suffix := "channel-" + channelConversationRouteHash(identity, spritzName)
	maxPrefixLen := 63 - len(suffix) - 1
	if maxPrefixLen < 1 {
		maxPrefixLen = 1
	}
	if len(prefix) > maxPrefixLen {
		prefix = prefix[:maxPrefixLen]
		prefix = strings.TrimRight(prefix, "-")
		if prefix == "" {
			prefix = "conversation"
			if len(prefix) > maxPrefixLen {
				prefix = prefix[:maxPrefixLen]
			}
		}
	}
	return fmt.Sprintf("%s-%s", prefix, suffix)
}

func channelConversationMatchesIdentity(conversation *spritzv1.SpritzConversation, identity normalizedChannelConversationIdentity) bool {
	if conversation == nil {
		return false
	}
	return strings.TrimSpace(conversation.Annotations[channelConversationPrincipalAnnotationKey]) == identity.principalID &&
		strings.TrimSpace(conversation.Annotations[channelConversationProviderAnnotationKey]) == identity.provider &&
		strings.TrimSpace(conversation.Annotations[channelConversationExternalScopeTypeAnnotationKey]) == identity.externalScopeType &&
		strings.TrimSpace(conversation.Annotations[channelConversationExternalTenantIDAnnotationKey]) == identity.externalTenantID &&
		strings.TrimSpace(conversation.Annotations[channelConversationExternalChannelIDAnnotationKey]) == identity.externalChannelID &&
		strings.TrimSpace(conversation.Annotations[channelConversationExternalConversationIDAnnotationKey]) == identity.externalConversationID
}

func (s *server) getServiceScopedACPReadySpritz(c echo.Context, namespace, instanceID, ownerID string) (*spritzv1.Spritz, error) {
	spritz := &spritzv1.Spritz{}
	if err := s.client.Get(c.Request().Context(), clientKey(namespace, instanceID), spritz); err != nil {
		return nil, err
	}
	if strings.TrimSpace(spritz.Spec.Owner.ID) != strings.TrimSpace(ownerID) {
		return nil, errForbidden
	}
	if !spritzSupportsACPConversations(spritz) {
		return nil, errACPUnavailable
	}
	return spritz, nil
}

func (s *server) findChannelConversation(c echo.Context, namespace string, spritz *spritzv1.Spritz, identity normalizedChannelConversationIdentity) (*spritzv1.SpritzConversation, bool, error) {
	list := &spritzv1.SpritzConversationList{}
	if err := s.client.List(
		c.Request().Context(),
		list,
		client.InNamespace(namespace),
		client.MatchingLabels{
			acpConversationLabelKey:          acpConversationLabelValue,
			acpConversationOwnerLabelKey:     ownerLabelValue(spritz.Spec.Owner.ID),
			acpConversationSpritzLabelKey:    spritz.Name,
			channelConversationRouteLabelKey: channelConversationRouteHash(identity, spritz.Name),
		},
	); err != nil {
		return nil, false, err
	}
	var match *spritzv1.SpritzConversation
	for i := range list.Items {
		item := &list.Items[i]
		if !channelConversationMatchesIdentity(item, identity) {
			continue
		}
		if match != nil {
			return nil, true, echo.NewHTTPError(http.StatusConflict, "channel conversation is ambiguous")
		}
		match = item.DeepCopy()
	}
	if match == nil {
		return nil, false, nil
	}
	return match, true, nil
}

func applyChannelConversationMetadata(conversation *spritzv1.SpritzConversation, identity normalizedChannelConversationIdentity, requestID string, spritz *spritzv1.Spritz) {
	if conversation.Labels == nil {
		conversation.Labels = map[string]string{}
	}
	conversation.Labels[acpConversationOwnerLabelKey] = ownerLabelValue(spritz.Spec.Owner.ID)
	conversation.Labels[acpConversationSpritzLabelKey] = spritz.Name
	conversation.Labels[acpConversationLabelKey] = acpConversationLabelValue
	conversation.Labels[channelConversationRouteLabelKey] = channelConversationRouteHash(identity, spritz.Name)

	if conversation.Annotations == nil {
		conversation.Annotations = map[string]string{}
	}
	conversation.Annotations[channelConversationPrincipalAnnotationKey] = identity.principalID
	conversation.Annotations[channelConversationProviderAnnotationKey] = identity.provider
	conversation.Annotations[channelConversationExternalScopeTypeAnnotationKey] = identity.externalScopeType
	conversation.Annotations[channelConversationExternalTenantIDAnnotationKey] = identity.externalTenantID
	conversation.Annotations[channelConversationExternalChannelIDAnnotationKey] = identity.externalChannelID
	conversation.Annotations[channelConversationExternalConversationIDAnnotationKey] = identity.externalConversationID
	if requestID != "" {
		conversation.Annotations[requestIDAnnotationKey] = requestID
	}
}

func (s *server) upsertChannelConversation(c echo.Context) error {
	if !s.acp.enabled {
		return writeError(c, http.StatusNotFound, "acp disabled")
	}
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}
	if s.auth.enabled() {
		if !principal.isService() && !principal.isAdminPrincipal() {
			return writeForbidden(c)
		}
		if principal.isService() && !principal.hasScope(scopeChannelConversationsUpsert) && !principal.isAdminPrincipal() {
			return writeForbidden(c)
		}
	}

	var body channelConversationUpsertRequest
	if err := decodeACPBody(c, &body); err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	normalizedBody, identity, err := normalizeChannelConversationUpsertRequest(principal.ID, body)
	if err != nil {
		if httpErr, ok := err.(*echo.HTTPError); ok {
			return writeError(c, httpErr.Code, httpErr.Message.(string))
		}
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	namespace, err := s.resolveSpritzNamespace(normalizedBody.Namespace)
	if err != nil {
		return writeError(c, http.StatusForbidden, err.Error())
	}

	spritz, err := s.getServiceScopedACPReadySpritz(c, namespace, normalizedBody.InstanceID, normalizedBody.OwnerID)
	if err != nil {
		return s.writeACPResourceError(c, err)
	}
	conversation, found, err := s.findChannelConversation(c, namespace, spritz, identity)
	if err != nil {
		if httpErr, ok := err.(*echo.HTTPError); ok {
			return writeError(c, httpErr.Code, httpErr.Message.(string))
		}
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	if found {
		return writeJSON(c, http.StatusOK, map[string]any{"created": false, "conversation": conversation})
	}

	conversation, err = buildACPConversationResource(spritz, normalizedBody.Title, normalizedBody.CWD)
	if err != nil {
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	conversation.Name = channelConversationName(spritz.Name, identity)
	applyChannelConversationMetadata(conversation, identity, normalizedBody.RequestID, spritz)
	if err := s.client.Create(c.Request().Context(), conversation); err != nil {
		if apierrors.IsAlreadyExists(err) {
			existing := &spritzv1.SpritzConversation{}
			if getErr := s.client.Get(c.Request().Context(), clientKey(namespace, conversation.Name), existing); getErr != nil {
				return writeError(c, http.StatusInternalServerError, getErr.Error())
			}
			if !channelConversationMatchesIdentity(existing, identity) {
				return writeError(c, http.StatusConflict, "channel conversation is ambiguous")
			}
			return writeJSON(c, http.StatusOK, map[string]any{"created": false, "conversation": existing})
		}
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	return writeJSON(c, http.StatusCreated, map[string]any{"created": true, "conversation": conversation})
}
