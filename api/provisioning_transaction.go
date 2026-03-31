package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	spritzv1 "spritz.sh/operator/api/v1"
)

type provisionerCreateError struct {
	status  int
	message string
	data    any
	err     error
}

func (e *provisionerCreateError) Error() string {
	return e.message
}

func (e *provisionerCreateError) Unwrap() error {
	return e.err
}

func newProvisionerCreateError(status int, err error) error {
	return &provisionerCreateError{
		status:  status,
		message: err.Error(),
		err:     err,
	}
}

func newProvisionerCreateErrorWithData(status int, message string, data any, err error) error {
	return &provisionerCreateError{
		status:  status,
		message: message,
		data:    data,
		err:     err,
	}
}

func newProvisionerForbiddenError() error {
	return &provisionerCreateError{
		status:  http.StatusForbidden,
		message: "forbidden",
		err:     errForbidden,
	}
}

func writeProvisionerCreateError(c echo.Context, err error) error {
	var provisionerErr *provisionerCreateError
	if errors.As(err, &provisionerErr) {
		if provisionerErr.data != nil {
			return writeJSendFailData(c, provisionerErr.status, provisionerErr.data)
		}
		return writeError(c, provisionerErr.status, provisionerErr.message)
	}
	return writeError(c, http.StatusInternalServerError, err.Error())
}

// provisionerCreateTransaction owns the service-principal create flow from
// canonical request normalization through idempotent replay/resume decisions.
type provisionerCreateTransaction struct {
	server                  *server
	ctx                     context.Context
	principal               principal
	namespace               string
	nameProvided            bool
	requestedImage          bool
	requestedRepo           bool
	requestedNamespace      bool
	normalizedUserConfig    json.RawMessage
	fingerprintRequest      createRequest
	body                    *createRequest
	provisionerFingerprint  string
	idempotencyState        provisionerIdempotencyState
	resolvedFromReservation bool
	resolvedExternalOwner   *externalOwnerResolution
	completed               bool
}

func newProvisionerCreateTransaction(
	server *server,
	ctx context.Context,
	principal principal,
	namespace string,
	body *createRequest,
	fingerprintRequest createRequest,
	normalizedUserConfig json.RawMessage,
	nameProvided bool,
	requestedImage, requestedRepo, requestedNamespace bool,
) *provisionerCreateTransaction {
	return &provisionerCreateTransaction{
		server:               server,
		ctx:                  ctx,
		principal:            principal,
		namespace:            namespace,
		nameProvided:         nameProvided,
		body:                 body,
		fingerprintRequest:   fingerprintRequest,
		normalizedUserConfig: normalizedUserConfig,
		requestedImage:       requestedImage,
		requestedRepo:        requestedRepo,
		requestedNamespace:   requestedNamespace,
	}
}

// prepare resolves the canonical provisioning request and applies idempotent
// replay/resume state before any create attempt happens.
func (tx *provisionerCreateTransaction) prepare() error {
	if err := authorizeServiceAction(tx.principal, scopeInstancesCreate, true); err != nil {
		return newProvisionerForbiddenError()
	}
	if err := authorizeServiceAction(tx.principal, scopeInstancesAssignOwner, true); err != nil {
		return newProvisionerForbiddenError()
	}
	if tx.fingerprintRequest.OwnerRef != nil && strings.EqualFold(strings.TrimSpace(tx.fingerprintRequest.OwnerRef.Type), "external") {
		if err := authorizeServiceAction(tx.principal, scopeExternalResolveViaCreate, true); err != nil {
			return newProvisionerForbiddenError()
		}
	}
	if strings.TrimSpace(tx.body.IdempotencyKey) == "" {
		return newProvisionerCreateError(http.StatusBadRequest, errors.New("idempotencyKey is required"))
	}
	externalIssuer := tx.server.externalOwnerIssuerForPrincipal(tx.principal)
	canonicalName := strings.TrimSpace(tx.fingerprintRequest.Name)
	canonicalNamePrefix := ""
	if canonicalName == "" {
		canonicalNamePrefix = strings.TrimSpace(tx.fingerprintRequest.NamePrefix)
	}
	fingerprint, err := createRequestFingerprintWithIssuer(tx.fingerprintRequest, externalIssuer, tx.namespace, canonicalName, canonicalNamePrefix, tx.normalizedUserConfig)
	if err != nil {
		return err
	}
	tx.provisionerFingerprint = fingerprint

	reservationName, completed, storedPayload, found, err := tx.server.getIdempotencyReservation(tx.ctx, tx.principal.ID, tx.body.IdempotencyKey, tx.provisionerFingerprint)
	if err != nil {
		if isProvisionerConflict(err) {
			return newProvisionerCreateError(http.StatusConflict, err)
		}
		return newProvisionerCreateError(http.StatusInternalServerError, err)
	}
	tx.completed = completed
	if found {
		if err := tx.restoreStoredPayload(storedPayload); err != nil {
			return err
		}
		tx.resolvedFromReservation = true
		tx.idempotencyState = provisionerIdempotencyState{
			canonicalFingerprint: tx.provisionerFingerprint,
			resolvedPayload:      strings.TrimSpace(storedPayload),
		}
		if strings.TrimSpace(reservationName) != "" {
			tx.body.Name = reservationName
		}
		return nil
	}

	owner, resolvedExternalOwner, err := tx.server.resolveCreateOwner(tx.ctx, tx.body, tx.principal)
	if err != nil {
		var resolutionErr externalOwnerResolutionError
		if errors.As(err, &resolutionErr) {
			return newProvisionerCreateErrorWithData(resolutionErr.status, resolutionErr.message, resolutionErr.responseData(), err)
		}
		if errors.Is(err, errForbidden) {
			return newProvisionerForbiddenError()
		}
		return newProvisionerCreateError(http.StatusBadRequest, err)
	}
	tx.body.Spec.Owner = owner
	tx.resolvedExternalOwner = resolvedExternalOwner
	if err := tx.server.validateProvisionerCreate(tx.ctx, tx.principal, tx.namespace, tx.body, tx.requestedImage, tx.requestedRepo, tx.requestedNamespace); err != nil {
		if errors.Is(err, errForbidden) {
			return newProvisionerForbiddenError()
		}
		return newProvisionerCreateError(http.StatusBadRequest, err)
	}
	if err := tx.server.resolveCreateAdmission(tx.ctx, tx.principal, tx.namespace, tx.body); err != nil {
		var admissionErr *admissionError
		if errors.As(err, &admissionErr) {
			return newProvisionerCreateErrorWithData(admissionErr.status, admissionErr.message, admissionErr.data, err)
		}
		return newProvisionerCreateError(http.StatusInternalServerError, err)
	}
	if err := resolveCreateLifetimes(&tx.body.Spec, tx.server.provisioners, true); err != nil {
		return newProvisionerCreateError(http.StatusBadRequest, err)
	}
	tx.idempotencyState, err = tx.server.provisionerIdempotencyFingerprints(tx.fingerprintRequest, *tx.body, tx.resolvedExternalOwner, externalIssuer, tx.namespace, tx.normalizedUserConfig)
	if err != nil {
		return newProvisionerCreateError(http.StatusInternalServerError, err)
	}
	reservedName, completed, storedPayload, err := tx.server.reserveIdempotentCreateName(tx.ctx, tx.namespace, tx.principal, tx.body.IdempotencyKey, tx.body.Name, tx.idempotencyState)
	if err != nil {
		if isProvisionerConflict(err) {
			return newProvisionerCreateError(http.StatusConflict, err)
		}
		return newProvisionerCreateError(http.StatusInternalServerError, err)
	}
	tx.completed = completed
	if strings.TrimSpace(storedPayload) != "" {
		if err := tx.restoreStoredPayload(storedPayload); err != nil {
			return err
		}
	}
	tx.body.Name = reservedName
	return nil
}

func (tx *provisionerCreateTransaction) restoreStoredPayload(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return newProvisionerCreateError(http.StatusConflict, errIdempotencyIncompatiblePending)
	}
	payload, err := decodeResolvedProvisionerPayload(raw)
	if err != nil {
		return newProvisionerCreateError(http.StatusInternalServerError, err)
	}
	tx.body.PresetID = payload.PresetID
	tx.body.NamePrefix = payload.NamePrefix
	tx.body.Source = payload.Source
	tx.body.RequestID = payload.RequestID
	tx.body.Spec = payload.Spec
	tx.body.Labels = cloneStringMap(payload.Labels)
	tx.body.Annotations = cloneStringMap(payload.Annotations)
	tx.resolvedExternalOwner = payload.ExternalOwner.resolution()
	return nil
}

func (tx *provisionerCreateTransaction) replayExisting() (*spritzv1.Spritz, error) {
	existing, err := tx.server.findReservedSpritz(tx.ctx, tx.namespace, tx.body.Name)
	if err != nil {
		return nil, newProvisionerCreateError(http.StatusInternalServerError, err)
	}
	if existing == nil {
		if tx.completed {
			if err := tx.invalidateStaleCompletedReplay(); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	if !matchesIdempotentReplayTarget(existing, tx.principal, tx.body.IdempotencyKey, tx.provisionerFingerprint) {
		if tx.completed {
			return nil, newProvisionerCreateError(http.StatusConflict, errIdempotencyUsedDifferent)
		}
		return nil, nil
	}
	return existing, nil
}

func (tx *provisionerCreateTransaction) invalidateStaleCompletedReplay() error {
	if err := tx.server.invalidateCompletedIdempotencyReservation(
		tx.ctx,
		tx.principal.ID,
		tx.body.IdempotencyKey,
		tx.provisionerFingerprint,
	); err != nil {
		if isProvisionerConflict(err) {
			return newProvisionerCreateError(http.StatusConflict, err)
		}
		return newProvisionerCreateError(http.StatusInternalServerError, err)
	}
	tx.completed = false
	if !tx.nameProvided {
		tx.body.Name = ""
	}
	return nil
}

func (tx *provisionerCreateTransaction) finalizeCreate() error {
	if tx.completed {
		return newProvisionerCreateError(http.StatusConflict, errIdempotencyUsed)
	}
	if !tx.resolvedFromReservation {
		if err := tx.server.enforceProvisionerQuotas(tx.ctx, tx.namespace, tx.principal, tx.body.Spec.Owner.ID); err != nil {
			return newProvisionerCreateError(http.StatusBadRequest, err)
		}
	}
	tx.body.Annotations = mergeStringMap(tx.body.Annotations, map[string]string{
		actorIDAnnotationKey:         tx.principal.ID,
		actorTypeAnnotationKey:       string(tx.principal.Type),
		sourceAnnotationKey:          provisionerSource(tx.body),
		requestIDAnnotationKey:       tx.body.RequestID,
		idempotencyKeyAnnotationKey:  tx.body.IdempotencyKey,
		idempotencyHashAnnotationKey: tx.provisionerFingerprint,
	})
	return nil
}

func (tx *provisionerCreateTransaction) reserveAttemptName(failedName, proposedName string) (string, *spritzv1.Spritz, error) {
	reservedName, completed, _, err := tx.server.setIdempotencyReservationName(tx.ctx, tx.principal.ID, tx.body.IdempotencyKey, failedName, proposedName, tx.idempotencyState)
	if err != nil {
		if isProvisionerConflict(err) {
			return "", nil, newProvisionerCreateError(http.StatusConflict, err)
		}
		return "", nil, newProvisionerCreateError(http.StatusInternalServerError, err)
	}
	if !completed {
		return reservedName, nil, nil
	}
	existing, err := tx.server.findReservedSpritz(tx.ctx, tx.namespace, reservedName)
	if err != nil {
		return "", nil, newProvisionerCreateError(http.StatusInternalServerError, err)
	}
	if matchesIdempotentReplayTarget(existing, tx.principal, tx.body.IdempotencyKey, tx.provisionerFingerprint) {
		return reservedName, existing, nil
	}
	return "", nil, newProvisionerCreateError(http.StatusConflict, errIdempotencyUsed)
}

func isProvisionerConflict(err error) bool {
	return errors.Is(err, errIdempotencyUsed) ||
		errors.Is(err, errIdempotencyUsedDifferent) ||
		errors.Is(err, errIdempotencyIncompatiblePending)
}
