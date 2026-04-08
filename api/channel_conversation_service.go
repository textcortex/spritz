package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

const (
	channelConversationRouteLabelKey                            = "spritz.sh/channel-route"
	channelConversationPrincipalAnnotationKey                   = "spritz.sh/channel-principal-id"
	channelConversationProviderAnnotationKey                    = "spritz.sh/channel-provider"
	channelConversationExternalScopeTypeAnnotationKey           = "spritz.sh/channel-external-scope-type"
	channelConversationExternalTenantIDAnnotationKey            = "spritz.sh/channel-external-tenant-id"
	channelConversationExternalChannelIDAnnotationKey           = "spritz.sh/channel-external-channel-id"
	channelConversationExternalConversationIDAnnotationKey      = "spritz.sh/channel-external-conversation-id"
	channelConversationExternalConversationAliasesAnnotationKey = "spritz.sh/channel-external-conversation-aliases"
	channelConversationBaseRouteLabelKey                        = "spritz.sh/channel-route-base"
)

type channelConversationUpsertRequest struct {
	RequestID                     string   `json:"requestId,omitempty"`
	Namespace                     string   `json:"namespace,omitempty"`
	ConversationID                string   `json:"conversationId,omitempty"`
	PrincipalID                   string   `json:"principalId"`
	InstanceID                    string   `json:"instanceId"`
	OwnerID                       string   `json:"ownerId"`
	Provider                      string   `json:"provider"`
	ExternalScopeType             string   `json:"externalScopeType"`
	ExternalTenantID              string   `json:"externalTenantId"`
	ExternalChannelID             string   `json:"externalChannelId"`
	ExternalConversationID        string   `json:"externalConversationId"`
	LookupExternalConversationIDs []string `json:"lookupExternalConversationIds,omitempty"`
	Title                         string   `json:"title,omitempty"`
	CWD                           string   `json:"cwd,omitempty"`
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
func normalizeChannelConversationUpsertRequest(body channelConversationUpsertRequest) (channelConversationUpsertRequest, normalizedChannelConversationIdentity, error) {
	body.RequestID = strings.TrimSpace(body.RequestID)
	body.Namespace = strings.TrimSpace(body.Namespace)
	body.ConversationID = strings.TrimSpace(body.ConversationID)
	body.PrincipalID = strings.TrimSpace(body.PrincipalID)
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
	if body.PrincipalID == "" {
		return channelConversationUpsertRequest{}, normalizedChannelConversationIdentity{}, echo.NewHTTPError(http.StatusBadRequest, "principalId is required")
	}
	body.ExternalChannelID = strings.TrimSpace(body.ExternalChannelID)
	if body.ExternalChannelID == "" {
		return channelConversationUpsertRequest{}, normalizedChannelConversationIdentity{}, echo.NewHTTPError(http.StatusBadRequest, "externalChannelId is required")
	}
	body.ExternalConversationID = strings.TrimSpace(body.ExternalConversationID)
	if body.ExternalConversationID == "" {
		return channelConversationUpsertRequest{}, normalizedChannelConversationIdentity{}, echo.NewHTTPError(http.StatusBadRequest, "externalConversationId is required")
	}
	body.LookupExternalConversationIDs = normalizeLookupExternalConversationIDs(
		body.ExternalConversationID,
		body.LookupExternalConversationIDs,
	)

	return body, normalizedChannelConversationIdentity{
		principalID:            body.PrincipalID,
		provider:               body.Provider,
		externalScopeType:      body.ExternalScopeType,
		externalTenantID:       body.ExternalTenantID,
		externalChannelID:      body.ExternalChannelID,
		externalConversationID: body.ExternalConversationID,
	}, nil
}

func normalizeLookupExternalConversationIDs(primaryID string, lookupIDs []string) []string {
	primaryID = strings.TrimSpace(primaryID)
	if len(lookupIDs) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(lookupIDs))
	seen := map[string]struct{}{}
	for _, lookupID := range lookupIDs {
		lookupID = strings.TrimSpace(lookupID)
		if lookupID == "" || lookupID == primaryID {
			continue
		}
		if _, ok := seen[lookupID]; ok {
			continue
		}
		seen[lookupID] = struct{}{}
		normalized = append(normalized, lookupID)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func channelConversationRouteHash(identity normalizedChannelConversationIdentity, ownerID, instanceID string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		identity.principalID,
		identity.provider,
		identity.externalScopeType,
		identity.externalTenantID,
		identity.externalChannelID,
		identity.externalConversationID,
		strings.TrimSpace(ownerID),
		strings.TrimSpace(instanceID),
	}, "\n")))
	return hex.EncodeToString(sum[:16])
}

func channelConversationBaseRouteHash(identity normalizedChannelConversationIdentity, ownerID, instanceID string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		identity.principalID,
		identity.provider,
		identity.externalScopeType,
		identity.externalTenantID,
		identity.externalChannelID,
		strings.TrimSpace(ownerID),
		strings.TrimSpace(instanceID),
	}, "\n")))
	return hex.EncodeToString(sum[:16])
}

func channelConversationName(spritzName, ownerID string, identity normalizedChannelConversationIdentity) string {
	prefix := strings.ToLower(strings.TrimSpace(spritzName))
	prefix = strings.Trim(prefix, "-")
	if prefix == "" {
		prefix = "conversation"
	}
	suffix := "channel-" + channelConversationRouteHash(identity, ownerID, spritzName)
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

func channelConversationMatchesBaseIdentity(conversation *spritzv1.SpritzConversation, identity normalizedChannelConversationIdentity) bool {
	if conversation == nil {
		return false
	}
	return strings.TrimSpace(conversation.Annotations[channelConversationPrincipalAnnotationKey]) == identity.principalID &&
		strings.TrimSpace(conversation.Annotations[channelConversationProviderAnnotationKey]) == identity.provider &&
		strings.TrimSpace(conversation.Annotations[channelConversationExternalScopeTypeAnnotationKey]) == identity.externalScopeType &&
		strings.TrimSpace(conversation.Annotations[channelConversationExternalTenantIDAnnotationKey]) == identity.externalTenantID &&
		strings.TrimSpace(conversation.Annotations[channelConversationExternalChannelIDAnnotationKey]) == identity.externalChannelID
}

func channelConversationExternalConversationAliases(conversation *spritzv1.SpritzConversation) []string {
	if conversation == nil {
		return nil
	}
	raw := strings.TrimSpace(conversation.Annotations[channelConversationExternalConversationAliasesAnnotationKey])
	if raw == "" {
		return nil
	}
	var payload []string
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}
	primary := strings.TrimSpace(conversation.Annotations[channelConversationExternalConversationIDAnnotationKey])
	aliases := make([]string, 0, len(payload))
	seen := map[string]struct{}{}
	for _, candidate := range payload {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || candidate == primary {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		aliases = append(aliases, candidate)
	}
	return aliases
}

func channelConversationHasExternalConversationID(conversation *spritzv1.SpritzConversation, externalConversationID string) bool {
	externalConversationID = strings.TrimSpace(externalConversationID)
	if externalConversationID == "" || conversation == nil {
		return false
	}
	if strings.TrimSpace(conversation.Annotations[channelConversationExternalConversationIDAnnotationKey]) == externalConversationID {
		return true
	}
	for _, alias := range channelConversationExternalConversationAliases(conversation) {
		if alias == externalConversationID {
			return true
		}
	}
	return false
}

func channelConversationHasAnyExternalConversationID(conversation *spritzv1.SpritzConversation, externalConversationIDs []string) bool {
	for _, externalConversationID := range externalConversationIDs {
		if channelConversationHasExternalConversationID(conversation, externalConversationID) {
			return true
		}
	}
	return false
}

func channelConversationMatchesIdentity(conversation *spritzv1.SpritzConversation, identity normalizedChannelConversationIdentity) bool {
	return channelConversationMatchesBaseIdentity(conversation, identity) &&
		channelConversationHasExternalConversationID(conversation, identity.externalConversationID)
}

func channelConversationBelongsToSpritz(conversation *spritzv1.SpritzConversation, spritz *spritzv1.Spritz) bool {
	if conversation == nil || spritz == nil {
		return false
	}
	return strings.TrimSpace(conversation.Spec.SpritzName) == spritz.Name &&
		strings.TrimSpace(conversation.Spec.Owner.ID) == spritz.Spec.Owner.ID &&
		strings.TrimSpace(conversation.Labels[acpConversationSpritzLabelKey]) == spritz.Name &&
		strings.TrimSpace(conversation.Labels[acpConversationOwnerLabelKey]) == ownerLabelValue(spritz.Spec.Owner.ID)
}

func appendChannelConversationAlias(conversation *spritzv1.SpritzConversation, externalConversationID string) (bool, error) {
	externalConversationID = strings.TrimSpace(externalConversationID)
	if externalConversationID == "" || conversation == nil {
		return false, nil
	}
	if conversation.Annotations == nil {
		conversation.Annotations = map[string]string{}
	}
	if channelConversationHasExternalConversationID(conversation, externalConversationID) {
		return false, nil
	}
	aliases := append(channelConversationExternalConversationAliases(conversation), externalConversationID)
	payload, err := json.Marshal(aliases)
	if err != nil {
		return false, err
	}
	conversation.Annotations[channelConversationExternalConversationAliasesAnnotationKey] = string(payload)
	return true, nil
}

func ensureChannelConversationBaseRouteLabel(conversation *spritzv1.SpritzConversation, identity normalizedChannelConversationIdentity, spritz *spritzv1.Spritz) bool {
	if conversation == nil || spritz == nil {
		return false
	}
	if conversation.Labels == nil {
		conversation.Labels = map[string]string{}
	}
	expected := channelConversationBaseRouteHash(identity, spritz.Spec.Owner.ID, spritz.Name)
	if strings.TrimSpace(conversation.Labels[channelConversationBaseRouteLabelKey]) == expected {
		return false
	}
	conversation.Labels[channelConversationBaseRouteLabelKey] = expected
	return true
}

func (s *server) getAdminScopedACPReadySpritz(c echo.Context, namespace, instanceID, ownerID string) (*spritzv1.Spritz, error) {
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

func (s *server) findChannelConversation(c echo.Context, namespace string, spritz *spritzv1.Spritz, identity normalizedChannelConversationIdentity, lookupExternalConversationIDs []string) (*spritzv1.SpritzConversation, bool, error) {
	matchExternalConversationIDs := append(
		[]string{identity.externalConversationID},
		lookupExternalConversationIDs...,
	)
	var match *spritzv1.SpritzConversation
	for _, externalConversationID := range matchExternalConversationIDs {
		candidateIdentity := identity
		candidateIdentity.externalConversationID = externalConversationID
		exactList := &spritzv1.SpritzConversationList{}
		if err := s.client.List(
			c.Request().Context(),
			exactList,
			client.InNamespace(namespace),
			client.MatchingLabels{
				acpConversationLabelKey:          acpConversationLabelValue,
				acpConversationOwnerLabelKey:     ownerLabelValue(spritz.Spec.Owner.ID),
				acpConversationSpritzLabelKey:    spritz.Name,
				channelConversationRouteLabelKey: channelConversationRouteHash(candidateIdentity, spritz.Spec.Owner.ID, spritz.Name),
			},
		); err != nil {
			return nil, false, err
		}
		for i := range exactList.Items {
			item := &exactList.Items[i]
			if !channelConversationMatchesBaseIdentity(item, identity) || !channelConversationHasAnyExternalConversationID(item, matchExternalConversationIDs) {
				continue
			}
			if match != nil && item.Name == match.Name {
				continue
			}
			if match != nil {
				return nil, true, echo.NewHTTPError(http.StatusConflict, "channel conversation is ambiguous")
			}
			match = item.DeepCopy()
		}
	}

	baseList := &spritzv1.SpritzConversationList{}
	if err := s.client.List(
		c.Request().Context(),
		baseList,
		client.InNamespace(namespace),
		client.MatchingLabels{
			acpConversationLabelKey:       acpConversationLabelValue,
			acpConversationOwnerLabelKey:  ownerLabelValue(spritz.Spec.Owner.ID),
			acpConversationSpritzLabelKey: spritz.Name,
			channelConversationBaseRouteLabelKey: channelConversationBaseRouteHash(
				identity,
				spritz.Spec.Owner.ID,
				spritz.Name,
			),
		},
	); err != nil {
		return nil, false, err
	}
	var baseRouteMatch *spritzv1.SpritzConversation
	for i := range baseList.Items {
		item := &baseList.Items[i]
		if !channelConversationMatchesBaseIdentity(item, identity) {
			continue
		}
		if channelConversationHasAnyExternalConversationID(item, matchExternalConversationIDs) {
			if match != nil && item.Name == match.Name {
				continue
			}
			if match != nil {
				return nil, true, echo.NewHTTPError(http.StatusConflict, "channel conversation is ambiguous")
			}
			match = item.DeepCopy()
			continue
		}

		// During the Slack cutover, a previously used channel may only have an
		// older per-thread/per-message identity. Reuse that lone base-route match
		// instead of forking a fresh channel-scoped conversation.
		if match != nil {
			continue
		}
		if baseRouteMatch != nil && item.Name == baseRouteMatch.Name {
			continue
		}
		if baseRouteMatch != nil {
			return nil, true, echo.NewHTTPError(http.StatusConflict, "channel conversation is ambiguous")
		}
		baseRouteMatch = item.DeepCopy()
	}
	if match != nil {
		return match, true, nil
	}
	if baseRouteMatch != nil {
		return baseRouteMatch, true, nil
	}
	return nil, false, nil
}

func applyChannelConversationMetadata(conversation *spritzv1.SpritzConversation, identity normalizedChannelConversationIdentity, requestID string, spritz *spritzv1.Spritz) {
	if conversation.Labels == nil {
		conversation.Labels = map[string]string{}
	}
	conversation.Labels[acpConversationOwnerLabelKey] = ownerLabelValue(spritz.Spec.Owner.ID)
	conversation.Labels[acpConversationSpritzLabelKey] = spritz.Name
	conversation.Labels[acpConversationLabelKey] = acpConversationLabelValue
	conversation.Labels[channelConversationRouteLabelKey] = channelConversationRouteHash(identity, spritz.Spec.Owner.ID, spritz.Name)
	conversation.Labels[channelConversationBaseRouteLabelKey] = channelConversationBaseRouteHash(identity, spritz.Spec.Owner.ID, spritz.Name)

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
