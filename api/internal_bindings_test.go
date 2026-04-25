package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

func TestInternalUpsertBindingCreatesAndFetchesBinding(t *testing.T) {
	s := newInternalSpritzesTestServer(t)
	personalAgent := s.instanceClasses.byID["personal-agent"]
	personalAgent.Creation.RequiredResolvedFields = []string{requiredResolvedFieldServiceAccountName}
	s.instanceClasses.byID["personal-agent"] = personalAgent

	resolverHits := 0
	resolver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resolverHits++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "resolved",
			"mutations": map[string]any{
				"spec": map[string]any{
					"serviceAccountName": "zeno-agent-user-123",
					"agentRef": map[string]any{
						"type":     "external",
						"provider": "example-agent-catalog",
						"id":       "ag-123",
					},
				},
			},
		})
	}))
	defer resolver.Close()

	s.extensions = extensionRegistry{
		resolvers: []configuredResolver{
			{
				id:            "test-zeno-binding",
				extensionType: extensionTypeResolver,
				operation:     extensionOperationPresetCreateResolve,
				match: extensionMatchRule{
					presetIDs: map[string]struct{}{"zeno": {}},
				},
				transport: configuredHTTPTransport{
					url:     resolver.URL,
					timeout: time.Second,
				},
			},
		},
	}
	e := echo.New()
	s.registerRoutes(e)

	body := `{
		"desiredRevision": "sha256:rev-1",
		"disconnected": false,
		"attributes": {
			"provider": "slack",
			"externalTenantId": "T_workspace_1"
		},
		"principal": {"id": "channel-gateway"},
		"request": {
			"presetId": "zeno",
			"ownerId": "user-123",
			"requestId": "binding-upsert-1",
			"source": "channel-gateway",
			"spec": {}
		}
	}`
	req := httptest.NewRequest(http.MethodPut, "/api/internal/v1/bindings/channel-installation-binding-1", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"bindingKey":"channel-installation-binding-1"`) {
		t.Fatalf("expected binding key in response, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"desiredRevision":"sha256:rev-1"`) {
		t.Fatalf("expected desired revision in response, got %s", rec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/internal/v1/bindings/channel-installation-binding-1", nil)
	getReq.Header.Set("Authorization", "Bearer spritz-internal-token")
	getRec := httptest.NewRecorder()
	e.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", getRec.Code, getRec.Body.String())
	}
	if !strings.Contains(getRec.Body.String(), `"bindingKey":"channel-installation-binding-1"`) {
		t.Fatalf("expected fetched binding key in response, got %s", getRec.Body.String())
	}
	if !strings.Contains(getRec.Body.String(), `"presetId":"zeno"`) {
		t.Fatalf("expected fetched preset id in response, got %s", getRec.Body.String())
	}

	var stored spritzv1.SpritzBinding
	if err := s.client.Get(
		context.Background(),
		client.ObjectKey{
			Namespace: "default",
			Name:      bindingResourceNameForKey("channel-installation-binding-1"),
		},
		&stored,
	); err != nil {
		t.Fatalf("failed to load stored binding: %v", err)
	}
	if stored.Spec.Template.Spec.ServiceAccountName != "zeno-agent-user-123" {
		t.Fatalf("expected resolved service account to be stored, got %#v", stored.Spec.Template.Spec)
	}
	if stored.Spec.Template.Spec.AgentRef == nil || stored.Spec.Template.Spec.AgentRef.ID != "ag-123" {
		t.Fatalf("expected resolved agent ref to be stored, got %#v", stored.Spec.Template.Spec.AgentRef)
	}
	if resolverHits != 1 {
		t.Fatalf("expected resolver to be called once, got %d", resolverHits)
	}
}

func TestInternalUpsertBindingPreservesNormalizedCreateAnnotations(t *testing.T) {
	s := newInternalSpritzesTestServer(t)
	e := echo.New()
	s.registerRoutes(e)

	body := `{
		"desiredRevision": "sha256:rev-1",
		"principal": {"id": "channel-gateway"},
		"request": {
			"presetId": "zeno",
			"ownerId": "user-123",
			"requestId": "binding-upsert-annotations",
			"source": "channel-gateway",
			"spec": {}
		}
	}`
	req := httptest.NewRequest(http.MethodPut, "/api/internal/v1/bindings/channel-installation-binding-annotations", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var stored spritzv1.SpritzBinding
	if err := s.client.Get(
		context.Background(),
		client.ObjectKey{
			Namespace: "default",
			Name:      bindingResourceNameForKey("channel-installation-binding-annotations"),
		},
		&stored,
	); err != nil {
		t.Fatalf("failed to load stored binding: %v", err)
	}

	if stored.Spec.Template.Annotations[instanceClassAnnotationKey] != "personal-agent" {
		t.Fatalf(
			"expected template to preserve %q, got %#v",
			instanceClassAnnotationKey,
			stored.Spec.Template.Annotations,
		)
	}
	if stored.Spec.Template.Annotations[instanceClassVersionAnnotationKey] != "v1" {
		t.Fatalf(
			"expected template to preserve %q, got %#v",
			instanceClassVersionAnnotationKey,
			stored.Spec.Template.Annotations,
		)
	}
}

func TestInternalUpsertBindingProjectsInstallationConfigIntoOpenClawEnv(t *testing.T) {
	s := newInternalSpritzesTestServer(t)
	s.presets.byID[0].Env = []corev1.EnvVar{{
		Name:  "OPENCLAW_CONFIG_JSON",
		Value: `{"channels":{"slack":{"groupPolicy":"allowlist","channels":{"C_OLD":{"allow":true,"requireMention":false}}}}}`,
	}}
	e := echo.New()
	s.registerRoutes(e)

	body := `{
		"desiredRevision": "sha256:rev-1",
		"attributes": {
			"provider": "slack",
			"externalScopeType": "workspace",
			"externalTenantId": "T021GRS5F4P"
		},
		"installationConfig": {
			"channelPolicies": [
				{"externalChannelId": "C0ANJGDB4Q5", "requireMention": false}
			]
		},
		"principal": {"id": "channel-gateway"},
		"request": {
			"presetId": "zeno",
			"ownerId": "user-123",
			"requestId": "binding-upsert-config",
			"source": "channel-gateway",
			"spec": {}
		}
	}`
	req := httptest.NewRequest(http.MethodPut, "/api/internal/v1/bindings/channel-installation-binding-config", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var stored spritzv1.SpritzBinding
	if err := s.client.Get(
		context.Background(),
		client.ObjectKey{
			Namespace: "default",
			Name:      bindingResourceNameForKey("channel-installation-binding-config"),
		},
		&stored,
	); err != nil {
		t.Fatalf("failed to load stored binding: %v", err)
	}

	var openClawConfig string
	for _, item := range stored.Spec.Template.Spec.Env {
		if item.Name == "OPENCLAW_CONFIG_JSON" {
			openClawConfig = item.Value
		}
	}
	var projected map[string]any
	if err := json.Unmarshal([]byte(openClawConfig), &projected); err != nil {
		t.Fatalf("expected valid OpenClaw config JSON, got %s: %v", openClawConfig, err)
	}
	channelsConfig, _ := projected["channels"].(map[string]any)
	slackConfig, _ := channelsConfig["slack"].(map[string]any)
	channels, _ := slackConfig["channels"].(map[string]any)
	channelConfig, _ := channels["C0ANJGDB4Q5"].(map[string]any)
	if channelConfig["allow"] != true || channelConfig["requireMention"] != false {
		t.Fatalf("expected OpenClaw channel policy in env, got %s", openClawConfig)
	}
	if slackConfig["groupPolicy"] != "allowlist" {
		t.Fatalf("expected existing Slack OpenClaw config to be preserved, got %s", openClawConfig)
	}
	if _, exists := channels["C_OLD"]; exists {
		t.Fatalf("expected stale OpenClaw channel policy to be removed, got %s", openClawConfig)
	}
}

func TestInternalUpsertBindingProjectsNullInstallationConfigAsEmptyPolicy(t *testing.T) {
	spec := spritzv1.SpritzSpec{
		Env: []corev1.EnvVar{{
			Name:  "OPENCLAW_CONFIG_JSON",
			Value: `{"channels":{"slack":{"channels":{"C_OLD":{"allow":true,"requireMention":false}}}}}`,
		}},
	}

	err := applyChannelInstallationConfigProjection(
		&spec,
		map[string]string{
			"provider":          "slack",
			"externalScopeType": "workspace",
			"externalTenantId":  "T021GRS5F4P",
		},
		json.RawMessage(`null`),
	)
	if err != nil {
		t.Fatalf("expected null installationConfig to clear policy projection, got %v", err)
	}

	var openClawConfig string
	for _, item := range spec.Env {
		if item.Name == "OPENCLAW_CONFIG_JSON" {
			openClawConfig = item.Value
		}
	}
	var projected map[string]any
	if err := json.Unmarshal([]byte(openClawConfig), &projected); err != nil {
		t.Fatalf("expected valid OpenClaw config JSON, got %s: %v", openClawConfig, err)
	}
	channelsConfig, _ := projected["channels"].(map[string]any)
	slackConfig, _ := channelsConfig["slack"].(map[string]any)
	channels, _ := slackConfig["channels"].(map[string]any)
	if len(channels) != 0 {
		t.Fatalf("expected null installationConfig to remove stale channels, got %s", openClawConfig)
	}
}

func TestInternalUpsertBindingProjectsInstallationConfigWithoutDroppingOpenClawDefaults(t *testing.T) {
	spec := spritzv1.SpritzSpec{}

	err := applyChannelInstallationConfigProjection(
		&spec,
		map[string]string{
			"provider":          "slack",
			"externalScopeType": "workspace",
			"externalTenantId":  "T021GRS5F4P",
		},
		json.RawMessage(`{"channelPolicies":[{"externalChannelId":"C0ANJGDB4Q5","requireMention":false}]}`),
	)
	if err != nil {
		t.Fatalf("expected installationConfig projection to succeed, got %v", err)
	}

	var openClawConfig string
	for _, item := range spec.Env {
		if item.Name == "OPENCLAW_CONFIG_JSON" {
			openClawConfig = item.Value
		}
	}
	var projected map[string]any
	if err := json.Unmarshal([]byte(openClawConfig), &projected); err != nil {
		t.Fatalf("expected valid OpenClaw config JSON, got %s: %v", openClawConfig, err)
	}
	browser, _ := projected["browser"].(map[string]any)
	if browser["enabled"] != true || browser["executablePath"] != "/usr/bin/chromium" {
		t.Fatalf("expected OpenClaw browser defaults to be preserved, got %s", openClawConfig)
	}
	channelsConfig, _ := projected["channels"].(map[string]any)
	slackConfig, _ := channelsConfig["slack"].(map[string]any)
	channels, _ := slackConfig["channels"].(map[string]any)
	channelConfig, _ := channels["C0ANJGDB4Q5"].(map[string]any)
	if channelConfig["allow"] != true || channelConfig["requireMention"] != false {
		t.Fatalf("expected OpenClaw channel policy in env, got %s", openClawConfig)
	}
}

func TestInternalDeleteBindingRemovesStoredBinding(t *testing.T) {
	s := newInternalSpritzesTestServer(t)
	e := echo.New()
	s.registerRoutes(e)

	body := `{
		"desiredRevision": "sha256:rev-1",
		"principal": {"id": "channel-gateway"},
		"request": {
			"presetId": "zeno",
			"ownerId": "user-123",
			"requestId": "binding-delete-1",
			"source": "channel-gateway",
			"spec": {}
		}
	}`
	putReq := httptest.NewRequest(http.MethodPut, "/api/internal/v1/bindings/channel-installation-binding-delete", strings.NewReader(body))
	putReq.Header.Set("Authorization", "Bearer spritz-internal-token")
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	e.ServeHTTP(putRec, putReq)

	if putRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", putRec.Code, putRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/internal/v1/bindings/channel-installation-binding-delete", nil)
	deleteReq.Header.Set("Authorization", "Bearer spritz-internal-token")
	deleteRec := httptest.NewRecorder()
	e.ServeHTTP(deleteRec, deleteReq)

	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", deleteRec.Code, deleteRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/internal/v1/bindings/channel-installation-binding-delete", nil)
	getReq.Header.Set("Authorization", "Bearer spritz-internal-token")
	getRec := httptest.NewRecorder()
	e.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d: %s", getRec.Code, getRec.Body.String())
	}
}

func TestInternalReplaceSpritzUsesBindingLifecycleWhenRuntimeIsOwnedByBinding(t *testing.T) {
	targetRevision := "sha256:rev-2"
	binding := &spritzv1.SpritzBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "channel-installation-binding-1",
			Namespace: "spritz-production",
		},
		Spec: spritzv1.SpritzBindingSpec{
			BindingKey:      "channel-installation-binding-1",
			DesiredRevision: "sha256:rev-1",
		},
		Status: spritzv1.SpritzBindingStatus{
			ObservedRevision: "sha256:rev-1",
			ActiveInstanceRef: &spritzv1.SpritzBindingInstanceRef{
				Namespace: "spritz-production",
				Name:      "zeno-acme",
				Revision:  "sha256:rev-1",
				Phase:     "Ready",
			},
			CandidateInstanceRef: &spritzv1.SpritzBindingInstanceRef{
				Namespace: "spritz-production",
				Name:      "zeno-replacement",
				Revision:  targetRevision,
				Phase:     "Provisioning",
			},
		},
	}
	source := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-acme",
			Namespace: "spritz-production",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: spritzv1.GroupVersion.String(),
				Kind:       "SpritzBinding",
				Name:       binding.Name,
			}},
		},
	}
	s := newInternalSpritzesTestServer(t, source)
	if err := s.client.Create(context.Background(), binding); err != nil {
		t.Fatalf("failed to create binding: %v", err)
	}
	e := echo.New()
	s.registerRoutes(e)

	body := `{
		"targetRevision": "sha256:rev-2",
		"idempotencyKey": "replace-1",
		"replacement": {
			"principal": {"id": "channel-gateway"},
			"request": {
				"presetId": "zeno",
				"ownerId": "user-123",
				"requestId": "replace-1",
				"source": "channel-gateway",
				"spec": {}
			}
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/internal/v1/spritzes/spritz-production/zeno-acme:replace", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Data internalReplaceSpritzResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode replace response: %v", err)
	}
	if payload.Data.Replacement.InstanceID != "zeno-replacement" {
		t.Fatalf("expected candidate replacement in response, got %#v", payload.Data)
	}
	if payload.Data.Replacement.TargetRevision != targetRevision {
		t.Fatalf("expected target revision %q, got %#v", targetRevision, payload.Data)
	}
	if payload.Data.Replayed {
		t.Fatalf("expected first binding replace to be non-replayed")
	}

	var stored spritzv1.SpritzBinding
	if err := s.client.Get(context.Background(), client.ObjectKey{Namespace: binding.Namespace, Name: binding.Name}, &stored); err != nil {
		t.Fatalf("failed to reload binding: %v", err)
	}
	if stored.Spec.DesiredRevision != targetRevision {
		t.Fatalf("expected binding desired revision to be updated, got %#v", stored.Spec)
	}
	if strings.TrimSpace(stored.Annotations[spritzv1.BindingReconcileRequestedAtAnnotationKey]) == "" {
		t.Fatalf("expected reconcile annotation to be set, got %#v", stored.Annotations)
	}
}

func TestInternalReplaceSpritzSchedulesBindingReplacementBeforeCandidateExists(t *testing.T) {
	targetRevision := "sha256:rev-2"
	binding := &spritzv1.SpritzBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "channel-installation-binding-1",
			Namespace: "spritz-production",
		},
		Spec: spritzv1.SpritzBindingSpec{
			BindingKey:      "channel-installation-binding-1",
			DesiredRevision: "sha256:rev-1",
			Template: spritzv1.SpritzBindingTemplate{
				PresetID:   "zeno",
				NamePrefix: "zeno",
			},
		},
		Status: spritzv1.SpritzBindingStatus{
			ObservedRevision: "sha256:rev-1",
			ActiveInstanceRef: &spritzv1.SpritzBindingInstanceRef{
				Namespace: "spritz-production",
				Name:      "zeno-acme",
				Revision:  "sha256:rev-1",
				Phase:     "Ready",
			},
		},
	}
	source := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-acme",
			Namespace: "spritz-production",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: spritzv1.GroupVersion.String(),
				Kind:       "SpritzBinding",
				Name:       binding.Name,
			}},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	s := newInternalSpritzesTestServer(t, source)
	if err := s.client.Create(context.Background(), binding); err != nil {
		t.Fatalf("failed to create binding: %v", err)
	}
	e := echo.New()
	s.registerRoutes(e)

	body := `{
		"targetRevision": "sha256:rev-2",
		"idempotencyKey": "replace-1",
		"replacement": {
			"principal": {"id": "channel-gateway"},
			"request": {
				"presetId": "zeno",
				"ownerId": "user-123",
				"requestId": "replace-1",
				"source": "channel-gateway",
				"spec": {}
			}
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/internal/v1/spritzes/spritz-production/zeno-acme:replace", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer spritz-internal-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	var firstPayload struct {
		Data internalReplaceSpritzResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &firstPayload); err != nil {
		t.Fatalf("failed to decode first replace response: %v", err)
	}
	if firstPayload.Data.Replacement.InstanceID == "" {
		t.Fatalf("expected a predicted replacement name, got %#v", firstPayload.Data)
	}
	if firstPayload.Data.Replacement.TargetRevision != targetRevision {
		t.Fatalf("expected target revision %q, got %#v", targetRevision, firstPayload.Data)
	}
	if firstPayload.Data.Replacement.Ready {
		t.Fatalf("expected predicted replacement to be unready")
	}
	if firstPayload.Data.Replayed {
		t.Fatalf("expected first request to be non-replayed")
	}

	replayReq := httptest.NewRequest(http.MethodPost, "/api/internal/v1/spritzes/spritz-production/zeno-acme:replace", strings.NewReader(body))
	replayReq.Header.Set("Authorization", "Bearer spritz-internal-token")
	replayReq.Header.Set("Content-Type", "application/json")
	replayRec := httptest.NewRecorder()
	e.ServeHTTP(replayRec, replayReq)

	if replayRec.Code != http.StatusAccepted {
		t.Fatalf("expected replay to stay accepted, got %d: %s", replayRec.Code, replayRec.Body.String())
	}
	var replayPayload struct {
		Data internalReplaceSpritzResponse `json:"data"`
	}
	if err := json.Unmarshal(replayRec.Body.Bytes(), &replayPayload); err != nil {
		t.Fatalf("failed to decode replay replace response: %v", err)
	}
	if replayPayload.Data.Replacement.InstanceID != firstPayload.Data.Replacement.InstanceID {
		t.Fatalf("expected replay to keep the same replacement identity, got first=%#v replay=%#v", firstPayload.Data, replayPayload.Data)
	}
	if !replayPayload.Data.Replayed {
		t.Fatalf("expected replay request to be marked replayed")
	}

	actorID := replaceReservationActorIDForTest("spritz-production", "zeno-acme")
	record, found, err := s.idempotencyReservations().get(context.Background(), actorID, "replace-1")
	if err != nil {
		t.Fatalf("failed to load replace reservation: %v", err)
	}
	if !found {
		t.Fatalf("expected replace reservation to be stored")
	}
	if !record.completed {
		t.Fatalf("expected replace reservation to be completed after the first response")
	}
	if record.name != firstPayload.Data.Replacement.InstanceID {
		t.Fatalf("expected reservation name %q, got %#v", firstPayload.Data.Replacement.InstanceID, record)
	}
}

func TestInternalReplaceSpritzBindingLifecycleRejectsIdempotencyConflicts(t *testing.T) {
	binding := &spritzv1.SpritzBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "channel-installation-binding-1",
			Namespace: "spritz-production",
		},
		Spec: spritzv1.SpritzBindingSpec{
			BindingKey:      "channel-installation-binding-1",
			DesiredRevision: "sha256:rev-1",
			Template: spritzv1.SpritzBindingTemplate{
				PresetID:   "zeno",
				NamePrefix: "zeno",
			},
		},
		Status: spritzv1.SpritzBindingStatus{
			ObservedRevision: "sha256:rev-1",
			ActiveInstanceRef: &spritzv1.SpritzBindingInstanceRef{
				Namespace: "spritz-production",
				Name:      "zeno-acme",
				Revision:  "sha256:rev-1",
				Phase:     "Ready",
			},
			CandidateInstanceRef: &spritzv1.SpritzBindingInstanceRef{
				Namespace: "spritz-production",
				Name:      "zeno-replacement",
				Revision:  "sha256:rev-2",
				Phase:     "Provisioning",
			},
		},
	}
	source := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeno-acme",
			Namespace: "spritz-production",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: spritzv1.GroupVersion.String(),
				Kind:       "SpritzBinding",
				Name:       binding.Name,
			}},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
	s := newInternalSpritzesTestServer(t, source)
	if err := s.client.Create(context.Background(), binding); err != nil {
		t.Fatalf("failed to create binding: %v", err)
	}
	e := echo.New()
	s.registerRoutes(e)

	firstBody := `{
		"targetRevision": "sha256:rev-2",
		"idempotencyKey": "replace-1",
		"replacement": {
			"principal": {"id": "channel-gateway"},
			"request": {
				"presetId": "zeno",
				"ownerId": "user-123",
				"requestId": "replace-1",
				"source": "channel-gateway",
				"spec": {}
			}
		}
	}`
	firstReq := httptest.NewRequest(http.MethodPost, "/api/internal/v1/spritzes/spritz-production/zeno-acme:replace", strings.NewReader(firstBody))
	firstReq.Header.Set("Authorization", "Bearer spritz-internal-token")
	firstReq.Header.Set("Content-Type", "application/json")
	firstRec := httptest.NewRecorder()
	e.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusAccepted {
		t.Fatalf("expected first replace to succeed, got %d: %s", firstRec.Code, firstRec.Body.String())
	}

	conflictingBody := `{
		"targetRevision": "sha256:rev-3",
		"idempotencyKey": "replace-1",
		"replacement": {
			"principal": {"id": "channel-gateway"},
			"request": {
				"presetId": "zeno",
				"ownerId": "user-123",
				"requestId": "replace-1-conflict",
				"source": "channel-gateway",
				"spec": {}
			}
		}
	}`
	conflictReq := httptest.NewRequest(http.MethodPost, "/api/internal/v1/spritzes/spritz-production/zeno-acme:replace", strings.NewReader(conflictingBody))
	conflictReq.Header.Set("Authorization", "Bearer spritz-internal-token")
	conflictReq.Header.Set("Content-Type", "application/json")
	conflictRec := httptest.NewRecorder()
	e.ServeHTTP(conflictRec, conflictReq)

	if conflictRec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", conflictRec.Code, conflictRec.Body.String())
	}
	if !strings.Contains(conflictRec.Body.String(), "different request") {
		t.Fatalf("expected idempotency conflict, got %s", conflictRec.Body.String())
	}

	var stored spritzv1.SpritzBinding
	if err := s.client.Get(context.Background(), client.ObjectKey{Namespace: binding.Namespace, Name: binding.Name}, &stored); err != nil {
		t.Fatalf("failed to reload binding: %v", err)
	}
	if stored.Spec.DesiredRevision != "sha256:rev-2" {
		t.Fatalf("expected binding to keep the original desired revision, got %#v", stored.Spec)
	}
}
