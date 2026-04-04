package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

type createACPConversationRequest struct {
	SpritzName string `json:"spritzName"`
	Title      string `json:"title,omitempty"`
	CWD        string `json:"cwd,omitempty"`
}

type updateACPConversationRequest struct {
	Title *string `json:"title,omitempty"`
	CWD   *string `json:"cwd,omitempty"`
}

func (s *server) listACPConversations(c echo.Context) error {
	if !s.acp.enabled {
		return writeError(c, http.StatusNotFound, "acp disabled")
	}
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}

	namespace := s.requestNamespace(c)
	spritzName := strings.TrimSpace(c.QueryParam("spritz"))
	if err := authorizeHumanOnly(principal, s.auth.enabled()); err != nil {
		return writeForbidden(c)
	}
	list := &spritzv1.SpritzConversationList{}
	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	labels := map[string]string{acpConversationLabelKey: acpConversationLabelValue}
	if spritzName != "" {
		labels[acpConversationSpritzLabelKey] = spritzName
	}
	if s.auth.enabled() && !principal.isAdminPrincipal() {
		labels[acpConversationOwnerLabelKey] = ownerLabelValue(principal.ID)
	}
	opts = append(opts, client.MatchingLabels(labels))
	if err := s.client.List(c.Request().Context(), list, opts...); err != nil {
		return writeError(c, http.StatusInternalServerError, err.Error())
	}

	items := make([]spritzv1.SpritzConversation, 0, len(list.Items))
	for _, item := range list.Items {
		if err := authorizeHumanOwnedAccess(principal, item.Spec.Owner.ID, s.auth.enabled()); err != nil {
			continue
		}
		if spritzName != "" && item.Spec.SpritzName != spritzName {
			continue
		}
		items = append(items, *item.DeepCopy())
	}
	sortACPConversations(items)
	return writeJSON(c, http.StatusOK, map[string]any{"items": items})
}

func (s *server) getACPConversation(c echo.Context) error {
	if !s.acp.enabled {
		return writeError(c, http.StatusNotFound, "acp disabled")
	}
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}
	if err := authorizeHumanOnly(principal, s.auth.enabled()); err != nil {
		return writeForbidden(c)
	}
	conversation, err := s.getAuthorizedConversation(c.Request().Context(), principal, s.requestNamespace(c), c.Param("id"))
	if err != nil {
		return s.writeACPConversationError(c, err)
	}
	return writeJSON(c, http.StatusOK, conversation)
}

func (s *server) createACPConversation(c echo.Context) error {
	if !s.acp.enabled {
		return writeError(c, http.StatusNotFound, "acp disabled")
	}
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}
	if err := authorizeHumanOnly(principal, s.auth.enabled()); err != nil {
		return writeForbidden(c)
	}

	var body createACPConversationRequest
	if err := decodeACPBody(c, &body); err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	body.SpritzName = strings.TrimSpace(body.SpritzName)
	if body.SpritzName == "" {
		return writeError(c, http.StatusBadRequest, "spritzName is required")
	}

	namespace := s.requestNamespace(c)
	if namespace == "" {
		namespace = "default"
	}
	spritz, err := s.getAuthorizedACPReadySpritz(c.Request().Context(), principal, namespace, body.SpritzName)
	if err != nil {
		return s.writeACPResourceError(c, err)
	}

	conversation, err := buildACPConversationResource(spritz, body.Title, body.CWD)
	if err != nil {
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := s.client.Create(c.Request().Context(), conversation); err == nil {
			return writeJSON(c, http.StatusCreated, conversation)
		} else if !apierrors.IsAlreadyExists(err) {
			return writeError(c, http.StatusInternalServerError, err.Error())
		}
		conversation.Name, err = newConversationName(spritz.Name)
		if err != nil {
			return writeError(c, http.StatusInternalServerError, err.Error())
		}
	}
	return writeError(c, http.StatusConflict, "failed to allocate conversation id")
}

func (s *server) updateACPConversation(c echo.Context) error {
	if !s.acp.enabled {
		return writeError(c, http.StatusNotFound, "acp disabled")
	}
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}
	if err := authorizeHumanOnly(principal, s.auth.enabled()); err != nil {
		return writeForbidden(c)
	}
	conversation, err := s.getAuthorizedConversation(c.Request().Context(), principal, s.requestNamespace(c), c.Param("id"))
	if err != nil {
		return s.writeACPConversationError(c, err)
	}

	var body updateACPConversationRequest
	if err := decodeACPBody(c, &body); err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}

	changed := false
	if body.Title != nil && conversation.Spec.Title != strings.TrimSpace(*body.Title) {
		conversation.Spec.Title = strings.TrimSpace(*body.Title)
		if conversation.Spec.Title == "" {
			conversation.Spec.Title = defaultACPConversationTitle
		}
		changed = true
	}
	if body.CWD != nil {
		nextCWD := normalizeConversationCWD(*body.CWD)
		nextExplicit := nextCWD != ""
		currentExplicit := conversationHasExplicitCWDOverride(conversation)
		if conversation.Spec.CWD != nextCWD || currentExplicit != nextExplicit {
			setConversationCWDOverride(conversation, *body.CWD)
			changed = true
		}
	}
	if changed {
		if err := s.client.Update(c.Request().Context(), conversation); err != nil {
			return writeError(c, http.StatusInternalServerError, err.Error())
		}
	}
	return writeJSON(c, http.StatusOK, conversation)
}

func decodeACPBody(c echo.Context, target any) error {
	if c.Request().Body == nil || c.Request().ContentLength == 0 {
		return nil
	}
	decoder := json.NewDecoder(c.Request().Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func (s *server) getAuthorizedConversation(ctx context.Context, principal principal, namespace, id string) (*spritzv1.SpritzConversation, error) {
	name := strings.TrimSpace(id)
	if name == "" {
		return nil, apierrors.NewNotFound(spritzv1.GroupVersion.WithResource("spritzconversations").GroupResource(), "")
	}
	if namespace == "" {
		namespace = "default"
	}
	conversation := &spritzv1.SpritzConversation{}
	if err := s.client.Get(ctx, clientKey(namespace, name), conversation); err != nil {
		return nil, err
	}
	if err := authorizeHumanOwnedAccess(principal, conversation.Spec.Owner.ID, s.auth.enabled()); err != nil {
		return nil, errForbidden
	}
	return conversation, nil
}

func authorizeACPConversationAccess(principal principal, conversation *spritzv1.SpritzConversation, enabled bool) error {
	if !enabled {
		return nil
	}
	if principal.isAdminPrincipal() {
		return nil
	}
	if principalCanAccessOwner(principal, conversation.Spec.Owner.ID) {
		return nil
	}
	if principal.isService() &&
		principal.hasScope(scopeChannelConversationsUpsert) &&
		strings.TrimSpace(conversation.Annotations[channelConversationPrincipalAnnotationKey]) == stringsTrim(principal.ID) {
		return nil
	}
	return errForbidden
}

func (s *server) getAuthorizedACPConversation(ctx context.Context, principal principal, namespace, id string) (*spritzv1.SpritzConversation, error) {
	name := strings.TrimSpace(id)
	if name == "" {
		return nil, apierrors.NewNotFound(spritzv1.GroupVersion.WithResource("spritzconversations").GroupResource(), "")
	}
	if namespace == "" {
		namespace = "default"
	}
	conversation := &spritzv1.SpritzConversation{}
	if err := s.client.Get(ctx, clientKey(namespace, name), conversation); err != nil {
		return nil, err
	}
	if err := authorizeACPConversationAccess(principal, conversation, s.auth.enabled()); err != nil {
		return nil, errForbidden
	}
	return conversation, nil
}

func (s *server) writeACPConversationError(c echo.Context, err error) error {
	switch {
	case apierrors.IsNotFound(err):
		return writeError(c, http.StatusNotFound, "conversation not found")
	case errors.Is(err, errForbidden):
		return writeError(c, http.StatusForbidden, "forbidden")
	default:
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
}
