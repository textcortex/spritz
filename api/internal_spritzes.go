package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

type internalCreateSpritzRequest struct {
	Principal internalCreatePrincipal `json:"principal"`
	Request   json.RawMessage         `json:"request"`
}

type internalCreatePrincipal struct {
	ID string `json:"id"`
}

type internalSpritzSummary struct {
	Metadata       internalSpritzMetadata `json:"metadata"`
	Spec           internalSpritzSpec     `json:"spec"`
	Status         spritzv1.SpritzStatus  `json:"status"`
	TargetRevision string                 `json:"targetRevision,omitempty"`
	AccessURL      string                 `json:"accessUrl,omitempty"`
	ChatURL        string                 `json:"chatUrl,omitempty"`
	InstanceURL    string                 `json:"instanceUrl,omitempty"`
}

type internalSpritzMetadata struct {
	Name              string      `json:"name"`
	Namespace         string      `json:"namespace"`
	CreationTimestamp metav1.Time `json:"creationTimestamp,omitempty"`
}

type internalSpritzSpec struct {
	Owner spritzv1.SpritzOwner `json:"owner"`
}

type internalReplaceSpritzRequest struct {
	TargetRevision string                      `json:"targetRevision"`
	IdempotencyKey string                      `json:"idempotencyKey"`
	Replacement    internalCreateSpritzRequest `json:"replacement"`
}

type internalReplaceSpritzResponse struct {
	Source      internalReplaceSource      `json:"source"`
	Replacement internalReplaceReplacement `json:"replacement"`
	Replayed    bool                       `json:"replayed"`
}

type internalReplaceSource struct {
	Namespace  string `json:"namespace"`
	InstanceID string `json:"instanceId"`
}

type internalReplaceReplacement struct {
	Namespace      string `json:"namespace"`
	InstanceID     string `json:"instanceId"`
	TargetRevision string `json:"targetRevision"`
	Phase          string `json:"phase,omitempty"`
	Ready          bool   `json:"ready"`
}

func (p internalCreatePrincipal) normalize() (principal, error) {
	id := strings.TrimSpace(p.ID)
	if id == "" {
		return principal{}, fmt.Errorf("principal.id is required")
	}
	return finalizePrincipal(
		id,
		"",
		nil,
		"",
		id,
		principalTypeService,
		[]string{
			scopeInstancesCreate,
			scopeInstancesAssignOwner,
			scopeExternalResolveViaCreate,
		},
		false,
	), nil
}

func summarizeInternalSpritz(spritz *spritzv1.Spritz) internalSpritzSummary {
	owner := spritz.Spec.Owner
	if hasExternalOwnerAnnotations(spritz.GetAnnotations()) {
		owner.ID = ""
	}
	return internalSpritzSummary{
		Metadata: internalSpritzMetadata{
			Name:              spritz.Name,
			Namespace:         spritz.Namespace,
			CreationTimestamp: spritz.CreationTimestamp,
		},
		Spec: internalSpritzSpec{
			Owner: owner,
		},
		Status:         spritz.Status,
		TargetRevision: strings.TrimSpace(spritz.GetAnnotations()[targetRevisionAnnotationKey]),
		AccessURL:      spritzv1.AccessURLForSpritz(spritz),
		ChatURL:        spritzv1.ChatURLForSpritz(spritz),
		InstanceURL:    spritzv1.InstanceURLForSpritz(spritz),
	}
}

func (s *server) createInternalSpritz(c echo.Context) error {
	var body internalCreateSpritzRequest
	if err := c.Bind(&body); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid json")
	}
	internalPrincipal, err := body.Principal.normalize()
	if err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	encodedRequest := bytes.TrimSpace(body.Request)
	if len(encodedRequest) == 0 {
		return writeError(c, http.StatusBadRequest, "request is required")
	}

	clonedRequest := c.Request().Clone(c.Request().Context())
	clonedRequest.Body = io.NopCloser(bytes.NewReader(encodedRequest))
	clonedRequest.ContentLength = int64(len(encodedRequest))
	c.SetRequest(clonedRequest)
	c.Set(principalContextKey, internalPrincipal)
	return s.createSpritz(c)
}

func (s *server) deleteInternalSpritz(c echo.Context) error {
	namespace, err := s.resolveSpritzNamespace(strings.TrimSpace(c.Param("namespace")))
	if err != nil {
		return writeError(c, http.StatusForbidden, err.Error())
	}
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		return writeError(c, http.StatusBadRequest, "name required")
	}

	var spritz spritzv1.Spritz
	if err := s.client.Get(c.Request().Context(), client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, &spritz); err != nil {
		if apierrors.IsNotFound(err) {
			return writeError(c, http.StatusNotFound, "not found")
		}
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	if err := s.client.Delete(c.Request().Context(), &spritz); err != nil {
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *server) getInternalSpritz(c echo.Context) error {
	namespace, err := s.resolveSpritzNamespace(strings.TrimSpace(c.Param("namespace")))
	if err != nil {
		return writeError(c, http.StatusForbidden, err.Error())
	}
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		return writeError(c, http.StatusBadRequest, "name required")
	}

	var spritz spritzv1.Spritz
	if err := s.client.Get(c.Request().Context(), client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, &spritz); err != nil {
		if apierrors.IsNotFound(err) {
			return writeError(c, http.StatusNotFound, "not found")
		}
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	return writeJSON(c, http.StatusOK, summarizeInternalSpritz(&spritz))
}

func (s *server) replaceInternalSpritz(c echo.Context) error {
	namespace, sourceName, err := parseInternalReplacePath(s, c)
	if err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}

	var body internalReplaceSpritzRequest
	if err := c.Bind(&body); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid json")
	}
	body.TargetRevision = strings.TrimSpace(body.TargetRevision)
	body.IdempotencyKey = strings.TrimSpace(body.IdempotencyKey)
	if body.TargetRevision == "" {
		return writeError(c, http.StatusBadRequest, "targetRevision is required")
	}
	if body.IdempotencyKey == "" {
		return writeError(c, http.StatusBadRequest, "idempotencyKey is required")
	}

	replacementPrincipal, replacementRequest, replacementFingerprint, err := s.normalizeReplaceRequest(
		c.Request().Context(),
		body,
		namespace,
		sourceName,
	)
	if err != nil {
		var requestErr *createRequestError
		if errors.As(err, &requestErr) {
			return writeCreateRequestError(c, err)
		}
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	if strings.TrimSpace(replacementRequest.Name) == sourceName {
		return writeError(c, http.StatusBadRequest, "replacement must not reuse the source instance name")
	}

	replaceReservationFingerprint := replacementRequestFingerprint(
		replacementPrincipal,
		body.TargetRevision,
		replacementFingerprint,
	)
	reservation, err := s.ensureReplaceReservation(
		c.Request().Context(),
		namespace,
		sourceName,
		body.IdempotencyKey,
		replaceReservationFingerprint,
	)
	if err != nil {
		if isProvisionerConflict(err) {
			return writeError(c, http.StatusConflict, errIdempotencyUsedDifferent.Error())
		}
		return writeError(c, http.StatusInternalServerError, err.Error())
	}

	sourceSummary := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      sourceName,
		},
	}
	sourceExists := true
	if err := s.client.Get(c.Request().Context(), client.ObjectKey{
		Namespace: namespace,
		Name:      sourceName,
	}, sourceSummary); err != nil {
		if apierrors.IsNotFound(err) {
			sourceExists = false
		} else {
			return writeError(c, http.StatusInternalServerError, err.Error())
		}
	}
	if sourceExists {
		binding, err := s.getBindingByRuntime(c.Request().Context(), namespace, sourceSummary)
		if err != nil {
			return writeError(c, http.StatusInternalServerError, err.Error())
		}
		if binding != nil {
			storedBinding, replayed, err := s.replaceInternalBinding(
				c.Request().Context(),
				binding,
				body.TargetRevision,
			)
			if err != nil {
				return writeError(c, http.StatusInternalServerError, err.Error())
			}
			replacement := replacementRuntimeFromBinding(storedBinding, strings.TrimSpace(reservation.name))
			if replacement == nil {
				return writeError(c, http.StatusServiceUnavailable, "binding replacement is unavailable")
			}
			if err := s.completeReplaceReservation(
				c.Request().Context(),
				namespace,
				sourceName,
				body.IdempotencyKey,
				replaceReservationFingerprint,
				replacement.Name,
			); err != nil {
				if isProvisionerConflict(err) {
					return writeError(c, http.StatusConflict, errIdempotencyUsedDifferent.Error())
				}
				return writeError(c, http.StatusInternalServerError, err.Error())
			}
			return writeReplaceResponse(c, sourceSummary, replacement, replayed)
		}
	}
	if strings.TrimSpace(reservation.name) != "" {
		existingReplacement, err := s.findReservedReplacementReplayTarget(
			c.Request().Context(),
			namespace,
			reservation.name,
			sourceName,
			body.IdempotencyKey,
			body.TargetRevision,
			replacementPrincipal,
			replacementFingerprint,
		)
		if err != nil {
			return writeError(c, http.StatusInternalServerError, err.Error())
		}
		if existingReplacement != nil {
			if err := s.client.Get(c.Request().Context(), client.ObjectKey{
				Namespace: namespace,
				Name:      sourceName,
			}, sourceSummary); err != nil {
				if !apierrors.IsNotFound(err) {
					return writeError(c, http.StatusInternalServerError, err.Error())
				}
			} else if err := validateCreatedReplacement(
				sourceSummary,
				existingReplacement,
				replacementPrincipal,
				namespace,
				sourceName,
				body.IdempotencyKey,
				body.TargetRevision,
				replacementFingerprint,
			); err != nil {
				return writeError(c, http.StatusConflict, err.Error())
			}
			return writeReplaceResponse(c, sourceSummary, existingReplacement, true)
		}
	}
	if !sourceExists {
		return writeError(c, http.StatusNotFound, "not found")
	}
	recorder, err := s.invokeCreateSpritzWithPrincipal(
		c,
		replacementPrincipal,
		replacementRequest,
		true,
	)
	if err != nil {
		return err
	}
	if recorder.Code != http.StatusOK && recorder.Code != http.StatusCreated {
		return c.Blob(recorder.Code, echo.MIMEApplicationJSONCharsetUTF8, recorder.Body.Bytes())
	}

	createdReplacement, replayed, err := extractCreatedReplacement(recorder.Body.Bytes())
	if err != nil {
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	if err := validateCreatedReplacement(
		sourceSummary,
		createdReplacement,
		replacementPrincipal,
		namespace,
		sourceName,
		body.IdempotencyKey,
		body.TargetRevision,
		replacementFingerprint,
	); err != nil {
		if !replayed {
			if cleanupErr := s.deleteReplacementCreateResult(
				c.Request().Context(),
				createdReplacement,
			); cleanupErr != nil {
				return writeError(c, http.StatusInternalServerError, cleanupErr.Error())
			}
		}
		return writeError(c, http.StatusConflict, err.Error())
	}
	if err := s.completeReplaceReservation(
		c.Request().Context(),
		namespace,
		sourceName,
		body.IdempotencyKey,
		replaceReservationFingerprint,
		createdReplacement.Name,
	); err != nil {
		if isProvisionerConflict(err) {
			return writeError(c, http.StatusConflict, errIdempotencyUsedDifferent.Error())
		}
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	return writeReplaceResponse(c, sourceSummary, createdReplacement, replayed)
}

func parseInternalReplacePath(s *server, c echo.Context) (string, string, error) {
	namespace, err := s.resolveSpritzNamespace(strings.TrimSpace(c.Param("namespace")))
	if err != nil {
		return "", "", err
	}
	rawName := strings.TrimPrefix(strings.TrimSpace(c.Param("*")), "/")
	if rawName == "" {
		return "", "", fmt.Errorf("name required")
	}
	if !strings.HasSuffix(rawName, ":replace") {
		return "", "", fmt.Errorf("unsupported spritz operation")
	}
	name := strings.TrimSuffix(rawName, ":replace")
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("name required")
	}
	return namespace, name, nil
}

func (s *server) normalizeReplaceRequest(
	ctx context.Context,
	body internalReplaceSpritzRequest,
	namespace, sourceName string,
) (principal, createRequest, string, error) {
	replacementPrincipal, err := body.Replacement.Principal.normalize()
	if err != nil {
		return principal{}, createRequest{}, "", err
	}
	encodedRequest := bytes.TrimSpace(body.Replacement.Request)
	if len(encodedRequest) == 0 {
		return principal{}, createRequest{}, "", fmt.Errorf("replacement.request is required")
	}
	var replacementRequest createRequest
	if err := json.Unmarshal(encodedRequest, &replacementRequest); err != nil {
		return principal{}, createRequest{}, "", fmt.Errorf("replacement.request is invalid")
	}
	if strings.TrimSpace(replacementRequest.IdempotencyKey) != "" {
		return principal{}, createRequest{}, "", fmt.Errorf("replacement.request.idempotencyKey must be omitted")
	}
	replacementRequest.Namespace = namespace
	replacementRequest.IdempotencyKey = replacementCreateIdempotencyKey(namespace, sourceName, body.IdempotencyKey)

	annotations, err := mergeMetadataStrict(
		replacementRequest.Annotations,
		map[string]string{
			replacementSourceNSAnnotationKey:   namespace,
			replacementSourceNameAnnotationKey: sourceName,
			replacementIDKeyAnnotationKey:      body.IdempotencyKey,
			targetRevisionAnnotationKey:        body.TargetRevision,
		},
		"annotation",
	)
	if err != nil {
		return principal{}, createRequest{}, "", err
	}
	replacementRequest.Annotations = annotations

	normalized, err := s.normalizeCreateRequest(
		ctx,
		replacementPrincipal,
		replacementRequest,
		true,
	)
	if err != nil {
		return principal{}, createRequest{}, "", err
	}
	externalIssuer := s.externalOwnerIssuerForPrincipal(replacementPrincipal)
	canonicalName := strings.TrimSpace(normalized.fingerprintRequest.Name)
	canonicalNamePrefix := ""
	if canonicalName == "" {
		canonicalNamePrefix = strings.TrimSpace(normalized.fingerprintRequest.NamePrefix)
	}
	fingerprint, err := createRequestFingerprintWithIssuer(
		normalized.fingerprintRequest,
		externalIssuer,
		normalized.namespace,
		canonicalName,
		canonicalNamePrefix,
		normalized.normalizedUserConfig,
	)
	if err != nil {
		return principal{}, createRequest{}, "", err
	}
	return replacementPrincipal, replacementRequest, fingerprint, nil
}

func replacementCreateIdempotencyKey(namespace, sourceName, idempotencyKey string) string {
	return fmt.Sprintf("replace:%s:%s:%s", namespace, sourceName, strings.TrimSpace(idempotencyKey))
}

func replacementReservationActorID(namespace, sourceName string) string {
	return fmt.Sprintf("replace:%s:%s", strings.TrimSpace(namespace), strings.TrimSpace(sourceName))
}

func replacementRequestFingerprint(
	principal principal,
	targetRevision, replacementFingerprint string,
) string {
	payload := struct {
		PrincipalID            string `json:"principalId"`
		PrincipalType          string `json:"principalType"`
		TargetRevision         string `json:"targetRevision"`
		ReplacementFingerprint string `json:"replacementFingerprint"`
	}{
		PrincipalID:            strings.TrimSpace(principal.ID),
		PrincipalType:          string(principal.Type),
		TargetRevision:         strings.TrimSpace(targetRevision),
		ReplacementFingerprint: strings.TrimSpace(replacementFingerprint),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", sum[:])
}

func matchesReservedReplacementReplayTarget(
	replacement *spritzv1.Spritz,
	principal principal,
	namespace, sourceName, idempotencyKey, targetRevision, replacementFingerprint string,
) bool {
	if replacement == nil {
		return false
	}
	annotations := replacement.GetAnnotations()
	if strings.TrimSpace(annotations[replacementSourceNSAnnotationKey]) != strings.TrimSpace(namespace) {
		return false
	}
	if strings.TrimSpace(annotations[replacementSourceNameAnnotationKey]) != strings.TrimSpace(sourceName) {
		return false
	}
	if strings.TrimSpace(annotations[replacementIDKeyAnnotationKey]) != strings.TrimSpace(idempotencyKey) {
		return false
	}
	if strings.TrimSpace(annotations[targetRevisionAnnotationKey]) != strings.TrimSpace(targetRevision) {
		return false
	}
	return matchesIdempotentReplayTarget(
		replacement,
		principal,
		replacementCreateIdempotencyKey(namespace, sourceName, idempotencyKey),
		replacementFingerprint,
	)
}

func (s *server) findReservedReplacementReplayTarget(
	ctx context.Context,
	namespace, replacementName, sourceName, idempotencyKey, targetRevision string,
	principal principal,
	replacementFingerprint string,
) (*spritzv1.Spritz, error) {
	existingReplacement, err := s.findReservedSpritz(ctx, namespace, replacementName)
	if err != nil {
		return nil, err
	}
	if !matchesReservedReplacementReplayTarget(
		existingReplacement,
		principal,
		namespace,
		sourceName,
		idempotencyKey,
		targetRevision,
		replacementFingerprint,
	) {
		return nil, nil
	}
	return existingReplacement, nil
}

func (s *server) ensureReplaceReservation(
	ctx context.Context,
	namespace, sourceName, idempotencyKey, fingerprint string,
) (idempotencyReservationRecord, error) {
	actorID := replacementReservationActorID(namespace, sourceName)
	store := s.idempotencyReservations()
	record := idempotencyReservationRecord{
		fingerprint: strings.TrimSpace(fingerprint),
	}
	if err := store.create(ctx, actorID, idempotencyKey, record); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return idempotencyReservationRecord{}, err
		}
		existing, found, getErr := store.get(ctx, actorID, idempotencyKey)
		if getErr != nil {
			return idempotencyReservationRecord{}, getErr
		}
		if !found {
			return idempotencyReservationRecord{}, fmt.Errorf(
				"replace reservation %s disappeared",
				idempotencyReservationName(actorID, idempotencyKey),
			)
		}
		if strings.TrimSpace(existing.fingerprint) != strings.TrimSpace(fingerprint) {
			return idempotencyReservationRecord{}, errIdempotencyUsedDifferent
		}
		return existing, nil
	}
	return record, nil
}

func (s *server) completeReplaceReservation(
	ctx context.Context,
	namespace, sourceName, idempotencyKey, fingerprint, replacementName string,
) error {
	actorID := replacementReservationActorID(namespace, sourceName)
	_, err := s.idempotencyReservations().update(
		ctx,
		actorID,
		idempotencyKey,
		func(record *idempotencyReservationRecord) error {
			if strings.TrimSpace(record.fingerprint) != strings.TrimSpace(fingerprint) {
				return errIdempotencyUsedDifferent
			}
			record.name = strings.TrimSpace(replacementName)
			record.completed = true
			return nil
		},
	)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (s *server) invokeCreateSpritzWithPrincipal(
	parent echo.Context,
	principal principal,
	body createRequest,
	allowReplacementAnnotations bool,
) (*httptest.ResponseRecorder, error) {
	encodedBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req := httptest.NewRequest(http.MethodPost, s.apiPathPrefix()+"/spritzes", bytes.NewReader(encodedBody))
	req = req.WithContext(parent.Request().Context())
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	recorder := httptest.NewRecorder()
	createContext := parent.Echo().NewContext(req, recorder)
	createContext.Set(principalContextKey, principal)
	createContext.Set(
		allowReplacementAnnotationsContextKey,
		allowReplacementAnnotations,
	)
	createServer := s
	if namespace := strings.TrimSpace(body.Namespace); namespace != "" {
		scopedServer := *s
		scopedServer.namespace = namespace
		createServer = &scopedServer
	}
	if err := createServer.createSpritz(createContext); err != nil {
		return nil, err
	}
	return recorder, nil
}

func extractCreatedReplacement(raw []byte) (*spritzv1.Spritz, bool, error) {
	var envelope struct {
		Status string               `json:"status"`
		Data   createSpritzResponse `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, false, fmt.Errorf("replacement create response is invalid")
	}
	if envelope.Status != "success" || envelope.Data.Spritz == nil {
		return nil, false, fmt.Errorf("replacement create response is invalid")
	}
	return envelope.Data.Spritz, envelope.Data.Replayed, nil
}

func replacementPreservesSourceOwner(source, replacement *spritzv1.Spritz) bool {
	if source == nil || replacement == nil {
		return false
	}
	if strings.TrimSpace(source.Spec.Owner.ID) != strings.TrimSpace(replacement.Spec.Owner.ID) {
		return false
	}
	sourceAnnotations := source.GetAnnotations()
	replacementAnnotations := replacement.GetAnnotations()
	for _, key := range []string{
		externalOwnerIssuerAnnotationKey,
		externalOwnerProviderAnnotationKey,
		externalOwnerTenantAnnotationKey,
		externalOwnerSubjectHashAnnotationKey,
	} {
		if strings.TrimSpace(sourceAnnotations[key]) != strings.TrimSpace(replacementAnnotations[key]) {
			return false
		}
	}
	return true
}

func validateCreatedReplacement(
	source, replacement *spritzv1.Spritz,
	principal principal,
	namespace, sourceName, idempotencyKey, targetRevision, replacementFingerprint string,
) error {
	if !matchesReservedReplacementReplayTarget(
		replacement,
		principal,
		namespace,
		sourceName,
		idempotencyKey,
		targetRevision,
		replacementFingerprint,
	) {
		return fmt.Errorf("replacement create replay target is invalid")
	}
	if !replacementPreservesSourceOwner(source, replacement) {
		return fmt.Errorf("replacement owner does not match source")
	}
	return nil
}

func (s *server) deleteReplacementCreateResult(
	ctx context.Context,
	replacement *spritzv1.Spritz,
) error {
	if replacement == nil {
		return nil
	}
	if err := s.client.Delete(ctx, replacement); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to clean up invalid replacement: %w", err)
	}
	return nil
}

func writeReplaceResponse(
	c echo.Context,
	source, replacement *spritzv1.Spritz,
	replayed bool,
) error {
	replacementPhase := strings.TrimSpace(replacement.Status.Phase)
	ready := replacementPhase == "Ready"
	statusCode := http.StatusAccepted
	if ready {
		statusCode = http.StatusOK
	}
	return writeJSON(c, statusCode, internalReplaceSpritzResponse{
		Source: internalReplaceSource{
			Namespace:  source.Namespace,
			InstanceID: source.Name,
		},
		Replacement: internalReplaceReplacement{
			Namespace:      replacement.Namespace,
			InstanceID:     replacement.Name,
			TargetRevision: strings.TrimSpace(replacement.GetAnnotations()[targetRevisionAnnotationKey]),
			Phase:          replacementPhase,
			Ready:          ready,
		},
		Replayed: replayed,
	})
}
