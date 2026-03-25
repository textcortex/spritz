package main

import (
	"net/http"

	"github.com/labstack/echo/v4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	spritzv1 "spritz.sh/operator/api/v1"
)

func (s *server) upsertChannelConversation(c echo.Context) error {
	if !s.acp.enabled {
		return writeError(c, http.StatusNotFound, "acp disabled")
	}
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}
	if s.auth.enabled() {
		if !principal.isHuman() && !principal.isAdminPrincipal() {
			return writeForbidden(c)
		}
	}

	var body channelConversationUpsertRequest
	if err := decodeACPBody(c, &body); err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	normalizedBody, identity, err := normalizeChannelConversationUpsertRequest(body)
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

	var spritz *spritzv1.Spritz
	if principal.isAdminPrincipal() {
		spritz, err = s.getAdminScopedACPReadySpritz(c, namespace, normalizedBody.InstanceID, normalizedBody.OwnerID)
		if err != nil {
			return s.writeACPResourceError(c, err)
		}
	} else {
		if s.auth.enabled() && normalizedBody.OwnerID != principal.ID {
			return writeForbidden(c)
		}
		spritz, err = s.getAuthorizedACPReadySpritz(c.Request().Context(), principal, namespace, normalizedBody.InstanceID)
		if err != nil {
			return s.writeACPResourceError(c, err)
		}
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
	if normalizedBody.ConversationID != "" {
		existing := &spritzv1.SpritzConversation{}
		if err := s.client.Get(c.Request().Context(), clientKey(namespace, normalizedBody.ConversationID), existing); err != nil {
			return s.writeACPResourceError(c, err)
		}
		if !channelConversationMatchesBaseIdentity(existing, identity) || !channelConversationBelongsToSpritz(existing, spritz) {
			return writeError(c, http.StatusConflict, "channel conversation is ambiguous")
		}
		changed, err := appendChannelConversationAlias(existing, identity.externalConversationID)
		if err != nil {
			return writeError(c, http.StatusInternalServerError, err.Error())
		}
		if ensureChannelConversationBaseRouteLabel(existing, identity, spritz) {
			changed = true
		}
		if normalizedBody.RequestID != "" {
			if existing.Annotations == nil {
				existing.Annotations = map[string]string{}
			}
			existing.Annotations[requestIDAnnotationKey] = normalizedBody.RequestID
			changed = true
		}
		if changed {
			if err := s.client.Update(c.Request().Context(), existing); err != nil {
				return s.writeACPResourceError(c, err)
			}
		}
		return writeJSON(c, http.StatusOK, map[string]any{"created": false, "conversation": existing})
	}

	conversation, err = buildACPConversationResource(spritz, normalizedBody.Title, normalizedBody.CWD)
	if err != nil {
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	conversation.Name = channelConversationName(spritz.Name, spritz.Spec.Owner.ID, identity)
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
