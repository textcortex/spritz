package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

type internalBindingRequest struct {
	DesiredRevision    string                      `json:"desiredRevision,omitempty"`
	Disconnected       bool                        `json:"disconnected,omitempty"`
	Attributes         map[string]string           `json:"attributes,omitempty"`
	InstallationConfig json.RawMessage             `json:"installationConfig,omitempty"`
	Principal          internalCreatePrincipal     `json:"principal"`
	Request            json.RawMessage             `json:"request"`
	AdoptActive        *internalBindingInstanceRef `json:"adoptActive,omitempty"`
	AdoptedRevision    string                      `json:"adoptedRevision,omitempty"`
}

type internalBindingInstanceRef struct {
	Namespace  string `json:"namespace,omitempty"`
	InstanceID string `json:"instanceId,omitempty"`
	Revision   string `json:"revision,omitempty"`
}

type internalBindingMetadata struct {
	Name              string      `json:"name"`
	Namespace         string      `json:"namespace"`
	CreationTimestamp metav1.Time `json:"creationTimestamp,omitempty"`
}

type internalBindingTemplateSummary struct {
	PresetID    string              `json:"presetId,omitempty"`
	NamePrefix  string              `json:"namePrefix,omitempty"`
	Source      string              `json:"source,omitempty"`
	RequestID   string              `json:"requestId,omitempty"`
	OwnerID     string              `json:"ownerId,omitempty"`
	Spec        spritzv1.SpritzSpec `json:"spec"`
	Labels      map[string]string   `json:"labels,omitempty"`
	Annotations map[string]string   `json:"annotations,omitempty"`
}

type internalBindingSpecSummary struct {
	BindingKey      string                         `json:"bindingKey"`
	DesiredRevision string                         `json:"desiredRevision,omitempty"`
	Disconnected    bool                           `json:"disconnected,omitempty"`
	Attributes      map[string]string              `json:"attributes,omitempty"`
	Template        internalBindingTemplateSummary `json:"template"`
}

type internalBindingSummary struct {
	Metadata internalBindingMetadata      `json:"metadata"`
	Spec     internalBindingSpecSummary   `json:"spec"`
	Status   spritzv1.SpritzBindingStatus `json:"status"`
}

func bindingResourceNameForKey(bindingKey string) string {
	normalized := strings.TrimSpace(bindingKey)
	sum := sha256.Sum256([]byte(normalized))
	prefix := sanitizeSpritzNameToken(normalized)
	if prefix == "" {
		prefix = "binding"
	}
	if len(prefix) > 36 {
		prefix = prefix[:36]
		prefix = strings.TrimRight(prefix, "-")
	}
	return fmt.Sprintf("%s-%x", prefix, sum[:8])
}

func summarizeInternalBinding(binding *spritzv1.SpritzBinding) internalBindingSummary {
	templateSpec := spritzv1.SpritzSpec{}
	binding.Spec.Template.Spec.DeepCopyInto(&templateSpec)
	return internalBindingSummary{
		Metadata: internalBindingMetadata{
			Name:              binding.Name,
			Namespace:         binding.Namespace,
			CreationTimestamp: binding.CreationTimestamp,
		},
		Spec: internalBindingSpecSummary{
			BindingKey:      strings.TrimSpace(binding.Spec.BindingKey),
			DesiredRevision: strings.TrimSpace(binding.Spec.DesiredRevision),
			Disconnected:    binding.Spec.Disconnected,
			Attributes:      cloneStringMap(binding.Spec.Attributes),
			Template: internalBindingTemplateSummary{
				PresetID:    strings.TrimSpace(binding.Spec.Template.PresetID),
				NamePrefix:  strings.TrimSpace(binding.Spec.Template.NamePrefix),
				Source:      strings.TrimSpace(binding.Spec.Template.Source),
				RequestID:   strings.TrimSpace(binding.Spec.Template.RequestID),
				OwnerID:     strings.TrimSpace(binding.Spec.Template.Spec.Owner.ID),
				Spec:        templateSpec,
				Labels:      cloneStringMap(binding.Spec.Template.Labels),
				Annotations: cloneStringMap(binding.Spec.Template.Annotations),
			},
		},
		Status: binding.Status,
	}
}

func (s *server) getInternalBinding(c echo.Context) error {
	namespace, bindingName, err := s.resolveBindingPath(c)
	if err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	var binding spritzv1.SpritzBinding
	if err := s.client.Get(c.Request().Context(), client.ObjectKey{Namespace: namespace, Name: bindingName}, &binding); err != nil {
		if apierrors.IsNotFound(err) {
			return writeError(c, http.StatusNotFound, "not found")
		}
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	return writeJSON(c, http.StatusOK, summarizeInternalBinding(&binding))
}

func (s *server) deleteInternalBinding(c echo.Context) error {
	namespace, bindingName, err := s.resolveBindingPath(c)
	if err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	binding := &spritzv1.SpritzBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      bindingName,
		},
	}
	if err := s.client.Delete(c.Request().Context(), binding); err != nil {
		if apierrors.IsNotFound(err) {
			return writeError(c, http.StatusNotFound, "not found")
		}
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *server) upsertInternalBinding(c echo.Context) error {
	namespace, bindingName, err := s.resolveBindingPath(c)
	if err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}

	var body internalBindingRequest
	if err := c.Bind(&body); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid json")
	}
	internalPrincipal, err := body.Principal.normalize()
	if err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}

	var requestBody createRequest
	if err := json.Unmarshal(bytes.TrimSpace(body.Request), &requestBody); err != nil {
		return writeError(c, http.StatusBadRequest, "request is invalid")
	}
	if strings.TrimSpace(requestBody.Name) != "" {
		return writeError(c, http.StatusBadRequest, "request.name is not allowed for bindings")
	}

	normalized, err := s.normalizeCreateRequest(c.Request().Context(), internalPrincipal, requestBody, false)
	if err != nil {
		return writeCreateRequestError(c, err)
	}
	requestBody = normalized.body
	if err := s.resolveCreateAdmission(c.Request().Context(), internalPrincipal, namespace, &requestBody); err != nil {
		var admissionErr *admissionError
		if errors.As(err, &admissionErr) {
			if admissionErr.data != nil {
				return writeJSendFailData(c, admissionErr.status, admissionErr.data)
			}
			return writeError(c, admissionErr.status, admissionErr.message)
		}
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	if err := resolveCreateLifetimes(&requestBody.Spec, s.provisioners, true); err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	if err := applyChannelInstallationConfigProjection(&requestBody.Spec, body.Attributes, body.InstallationConfig); err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}

	if err := s.ensureServiceAccount(c.Request().Context(), namespace, requestBody.Spec.ServiceAccountName); err != nil {
		return writeError(c, http.StatusInternalServerError, "failed to ensure service account")
	}

	labels := map[string]string{
		ownerLabelKey: ownerLabelValue(requestBody.Spec.Owner.ID),
		actorLabelKey: actorLabelValue(internalPrincipal.ID),
	}
	if strings.TrimSpace(requestBody.PresetID) != "" {
		labels[presetLabelKey] = strings.TrimSpace(requestBody.PresetID)
	}

	annotations := mergeStringMap(s.defaultMetadata, requestBody.Annotations)
	if annotations == nil {
		annotations = map[string]string{}
	}
	if strings.TrimSpace(requestBody.PresetID) != "" {
		annotations[presetIDAnnotationKey] = strings.TrimSpace(requestBody.PresetID)
	}

	applySSHDefaults(&requestBody.Spec, s.sshDefaults, namespace)
	template := spritzv1.SpritzBindingTemplate{
		PresetID:    strings.TrimSpace(requestBody.PresetID),
		NamePrefix:  s.resolvedCreateNamePrefix(requestBody, normalized.requestedNamePrefix),
		Source:      provisionerSource(&requestBody),
		RequestID:   strings.TrimSpace(requestBody.RequestID),
		Spec:        requestBody.Spec,
		Labels:      labels,
		Annotations: annotations,
	}

	binding := &spritzv1.SpritzBinding{}
	createNew := false
	if err := s.client.Get(c.Request().Context(), client.ObjectKey{Namespace: namespace, Name: bindingName}, binding); err != nil {
		if !apierrors.IsNotFound(err) {
			return writeError(c, http.StatusInternalServerError, err.Error())
		}
		createNew = true
		binding = &spritzv1.SpritzBinding{
			TypeMeta: metav1.TypeMeta{Kind: "SpritzBinding", APIVersion: spritzv1.GroupVersion.String()},
			ObjectMeta: metav1.ObjectMeta{
				Name:      bindingName,
				Namespace: namespace,
			},
		}
	}

	binding.Spec = spritzv1.SpritzBindingSpec{
		BindingKey:        strings.TrimSpace(c.Param("bindingKey")),
		DesiredRevision:   strings.TrimSpace(body.DesiredRevision),
		Disconnected:      body.Disconnected,
		Attributes:        cloneStringMap(body.Attributes),
		Template:          template,
		AdoptActive:       convertInternalBindingRef(body.AdoptActive, namespace),
		AdoptedRevision:   strings.TrimSpace(body.AdoptedRevision),
		ObservedRequestID: strings.TrimSpace(requestBody.RequestID),
	}
	if binding.Annotations == nil {
		binding.Annotations = map[string]string{}
	}
	binding.Annotations[spritzv1.BindingReconcileRequestedAtAnnotationKey] = time.Now().UTC().Format(time.RFC3339Nano)

	if createNew {
		if err := s.client.Create(c.Request().Context(), binding); err != nil {
			return writeError(c, http.StatusInternalServerError, err.Error())
		}
	} else {
		if err := s.client.Update(c.Request().Context(), binding); err != nil {
			return writeError(c, http.StatusInternalServerError, err.Error())
		}
	}

	var stored spritzv1.SpritzBinding
	if err := s.client.Get(c.Request().Context(), client.ObjectKey{Namespace: namespace, Name: bindingName}, &stored); err != nil {
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	return writeJSON(c, http.StatusOK, summarizeInternalBinding(&stored))
}

func (s *server) reconcileInternalBinding(c echo.Context) error {
	namespace, bindingName, err := s.resolveBindingPath(c)
	if err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	var binding spritzv1.SpritzBinding
	if err := s.client.Get(c.Request().Context(), client.ObjectKey{Namespace: namespace, Name: bindingName}, &binding); err != nil {
		if apierrors.IsNotFound(err) {
			return writeError(c, http.StatusNotFound, "not found")
		}
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	if binding.Annotations == nil {
		binding.Annotations = map[string]string{}
	}
	binding.Annotations[spritzv1.BindingReconcileRequestedAtAnnotationKey] = time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.client.Update(c.Request().Context(), &binding); err != nil {
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	return writeJSON(c, http.StatusOK, summarizeInternalBinding(&binding))
}

func (s *server) resolveBindingPath(c echo.Context) (string, string, error) {
	namespace, err := s.resolveSpritzNamespace(strings.TrimSpace(c.Param("namespace")))
	if err != nil {
		return "", "", err
	}
	bindingKey := strings.TrimSpace(c.Param("bindingKey"))
	if bindingKey == "" {
		return "", "", fmt.Errorf("bindingKey required")
	}
	return namespace, bindingResourceNameForKey(bindingKey), nil
}

func convertInternalBindingRef(ref *internalBindingInstanceRef, defaultNamespace string) *spritzv1.SpritzBindingInstanceRef {
	if ref == nil {
		return nil
	}
	name := strings.TrimSpace(ref.InstanceID)
	if name == "" {
		return nil
	}
	namespace := strings.TrimSpace(ref.Namespace)
	if namespace == "" {
		namespace = defaultNamespace
	}
	return &spritzv1.SpritzBindingInstanceRef{
		Namespace: namespace,
		Name:      name,
		Revision:  strings.TrimSpace(ref.Revision),
	}
}

func findBindingOwner(spritz *spritzv1.Spritz) string {
	if spritz == nil {
		return ""
	}
	for _, owner := range spritz.OwnerReferences {
		if strings.EqualFold(strings.TrimSpace(owner.Kind), "SpritzBinding") && owner.Name != "" {
			return owner.Name
		}
	}
	if spritz.Labels != nil {
		return strings.TrimSpace(spritz.Labels[spritzv1.BindingNameLabelKey])
	}
	return ""
}

func (s *server) getBindingByRuntime(
	ctx context.Context,
	namespace string,
	source *spritzv1.Spritz,
) (*spritzv1.SpritzBinding, error) {
	bindingName := findBindingOwner(source)
	if bindingName == "" {
		return nil, nil
	}
	var binding spritzv1.SpritzBinding
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: bindingName}, &binding); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &binding, nil
}

func (s *server) replaceInternalBinding(
	ctx context.Context,
	binding *spritzv1.SpritzBinding,
	targetRevision string,
) (*spritzv1.SpritzBinding, bool, error) {
	if binding == nil {
		return nil, false, nil
	}
	desiredRevision := strings.TrimSpace(targetRevision)
	replayed := strings.TrimSpace(binding.Spec.DesiredRevision) == desiredRevision
	needsUpdate := false
	if binding.Spec.DesiredRevision != desiredRevision {
		binding.Spec.DesiredRevision = desiredRevision
		needsUpdate = true
	}
	if !bindingReadyOnDesiredRevision(binding) {
		if binding.Annotations == nil {
			binding.Annotations = map[string]string{}
		}
		binding.Annotations[spritzv1.BindingReconcileRequestedAtAnnotationKey] = time.Now().UTC().Format(time.RFC3339Nano)
		needsUpdate = true
	}
	if needsUpdate {
		if err := s.client.Update(ctx, binding); err != nil {
			return nil, false, err
		}
	}
	var stored spritzv1.SpritzBinding
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: binding.Namespace, Name: binding.Name}, &stored); err != nil {
		return nil, false, err
	}
	return &stored, replayed, nil
}

func replacementRuntimeFromBinding(binding *spritzv1.SpritzBinding, fallbackName string) *spritzv1.Spritz {
	if binding == nil {
		return nil
	}
	ref := binding.Status.CandidateInstanceRef
	if ref == nil && bindingReadyOnDesiredRevision(binding) {
		ref = binding.Status.ActiveInstanceRef
	}
	if ref != nil {
		revision := strings.TrimSpace(ref.Revision)
		if revision == "" {
			revision = bindingObservedRevision(binding)
		}
		if revision == "" {
			revision = strings.TrimSpace(binding.Spec.DesiredRevision)
		}
		return &spritzv1.Spritz{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: ref.Namespace,
				Name:      ref.Name,
				Annotations: map[string]string{
					targetRevisionAnnotationKey: revision,
				},
			},
			Status: spritzv1.SpritzStatus{
				Phase: strings.TrimSpace(ref.Phase),
			},
		}
	}
	name := strings.TrimSpace(fallbackName)
	if name == "" {
		name = predictedBindingCandidateName(binding)
	}
	if name == "" {
		return nil
	}
	revision := strings.TrimSpace(binding.Spec.DesiredRevision)
	if revision == "" {
		revision = bindingObservedRevision(binding)
	}
	return &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: binding.Namespace,
			Name:      name,
			Annotations: map[string]string{
				targetRevisionAnnotationKey: revision,
			},
		},
		Status: spritzv1.SpritzStatus{
			Phase: "Provisioning",
		},
	}
}

func bindingReadyOnDesiredRevision(binding *spritzv1.SpritzBinding) bool {
	if binding == nil || binding.Status.ActiveInstanceRef == nil {
		return false
	}
	if binding.Status.CandidateInstanceRef != nil {
		return false
	}
	return bindingObservedRevision(binding) == strings.TrimSpace(binding.Spec.DesiredRevision)
}

func bindingObservedRevision(binding *spritzv1.SpritzBinding) string {
	if binding == nil {
		return ""
	}
	if revision := strings.TrimSpace(binding.Status.ObservedRevision); revision != "" {
		return revision
	}
	if binding.Status.ActiveInstanceRef != nil {
		return strings.TrimSpace(binding.Status.ActiveInstanceRef.Revision)
	}
	return ""
}

func predictedBindingCandidateName(binding *spritzv1.SpritzBinding) string {
	if binding == nil {
		return ""
	}
	if binding.Status.CandidateInstanceRef != nil {
		return strings.TrimSpace(binding.Status.CandidateInstanceRef.Name)
	}
	return spritzv1.BindingRuntimeNameForSequence(
		strings.TrimSpace(binding.Spec.BindingKey),
		binding.Spec.Template.NamePrefix,
		binding.Spec.Template.PresetID,
		binding.Status.NextRuntimeSequence+1,
	)
}
