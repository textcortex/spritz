package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

type server struct {
	client                      client.Client
	clientset                   *kubernetes.Clientset
	restConfig                  *rest.Config
	scheme                      *runtime.Scheme
	namespace                   string
	controlNamespace            string
	auth                        authConfig
	internalAuth                internalAuthConfig
	ingressDefaults             ingressDefaults
	routeModel                  spritzv1.SharedHostRouteModel
	instanceProxy               instanceProxyConfig
	terminal                    terminalConfig
	sshGateway                  sshGatewayConfig
	sshDefaults                 sshDefaults
	sshMintLimiter              *sshMintLimiter
	acp                         acpConfig
	extensions                  extensionRegistry
	instanceClasses             instanceClassCatalog
	presets                     presetCatalog
	provisioners                provisionerPolicy
	externalOwners              externalOwnerConfig
	defaultMetadata             map[string]string
	sharedMounts                sharedMountsConfig
	sharedMountsStore           *sharedMountsStore
	sharedMountsLive            *sharedMountsLatestNotifier
	userConfigPolicy            userConfigPolicy
	instanceProxyTargetResolver func(*spritzv1.Spritz) (*url.URL, error)
	instanceProxyTransport      http.RoundTripper
	nameGeneratorFactory        func(context.Context, string, string) (func() string, error)
	activityRecorder            func(context.Context, string, string, time.Time) error
}

func main() {
	scheme := runtime.NewScheme()
	utilruntime.Must(spritzv1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))

	cfg := ctrl.GetConfigOrDie()
	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		os.Exit(1)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		os.Exit(1)
	}
	ns := os.Getenv("SPRITZ_NAMESPACE")
	controlNamespace := strings.TrimSpace(os.Getenv("SPRITZ_CONTROL_NAMESPACE"))
	if controlNamespace == "" {
		controlNamespace = strings.TrimSpace(ns)
	}
	if controlNamespace == "" {
		controlNamespace = strings.TrimSpace(os.Getenv("POD_NAMESPACE"))
	}
	if controlNamespace == "" {
		controlNamespace = "default"
	}
	{
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		opts := []client.ListOption{client.Limit(1)}
		if ns != "" {
			opts = append(opts, client.InNamespace(ns))
		}
		if err := k8sClient.List(ctx, &spritzv1.SpritzList{}, opts...); err != nil {
			fmt.Fprintf(os.Stderr, "k8s client not ready: %v\n", err)
			os.Exit(1)
		}
	}

	auth := newAuthConfig()
	if auth.configErr != nil {
		fmt.Fprintf(os.Stderr, "invalid auth config: %v\n", auth.configErr)
		os.Exit(1)
	}
	ingressDefaults := newIngressDefaults()
	routeModel := spritzRouteModelFromEnv()
	instanceProxy := newInstanceProxyConfig()
	terminal := newTerminalConfig()
	acp := newACPConfig()
	extensions, err := newExtensionRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid extension config: %v\n", err)
		os.Exit(1)
	}
	instanceClasses, err := newInstanceClassCatalog()
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid instance class config: %v\n", err)
		os.Exit(1)
	}
	presets, err := newPresetCatalog()
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid preset config: %v\n", err)
		os.Exit(1)
	}
	if err := instanceClasses.validatePresetCatalog(presets); err != nil {
		fmt.Fprintf(os.Stderr, "invalid preset config: %v\n", err)
		os.Exit(1)
	}
	provisioners := newProvisionerPolicy()
	externalOwners, err := newExternalOwnerConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid external owner config: %v\n", err)
		os.Exit(1)
	}
	sshDefaults := newSSHDefaults()
	sshGateway, err := newSSHGatewayConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid ssh gateway config: %v\n", err)
		os.Exit(1)
	}
	internalAuth := newInternalAuthConfig()
	sharedMounts, err := newSharedMountsConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid shared mounts config: %v\n", err)
		os.Exit(1)
	}
	userConfigPolicy := newUserConfigPolicy()
	if sharedMounts.enabled && !internalAuth.enabled {
		fmt.Fprintln(os.Stderr, "SPRITZ_INTERNAL_TOKEN must be set when shared mounts are enabled")
		os.Exit(1)
	}
	var sharedStore *sharedMountsStore
	if sharedMounts.enabled {
		sharedStore = newSharedMountsStore(sharedMounts)
	}
	var sharedMountsLive *sharedMountsLatestNotifier
	if sharedMounts.enabled {
		sharedMountsLive = newSharedMountsLatestNotifier()
	}
	sshMintLimiter := newSSHMintLimiter()
	defaultAnnotations, err := parseKeyValueCSV(os.Getenv("SPRITZ_DEFAULT_ANNOTATIONS"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid SPRITZ_DEFAULT_ANNOTATIONS: %v\n", err)
		os.Exit(1)
	}

	s := &server{
		client:            k8sClient,
		clientset:         clientset,
		restConfig:        cfg,
		scheme:            scheme,
		namespace:         ns,
		controlNamespace:  controlNamespace,
		auth:              auth,
		internalAuth:      internalAuth,
		ingressDefaults:   ingressDefaults,
		routeModel:        routeModel,
		instanceProxy:     instanceProxy,
		terminal:          terminal,
		sshGateway:        sshGateway,
		sshDefaults:       sshDefaults,
		sshMintLimiter:    sshMintLimiter,
		acp:               acp,
		extensions:        extensions,
		instanceClasses:   instanceClasses,
		presets:           presets,
		provisioners:      provisioners,
		externalOwners:    externalOwners,
		defaultMetadata:   defaultAnnotations,
		sharedMounts:      sharedMounts,
		sharedMountsStore: sharedStore,
		sharedMountsLive:  sharedMountsLive,
		userConfigPolicy:  userConfigPolicy,
	}

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(withRequestLogging())
	cors := newCORSConfig()
	if cors.enabled() {
		e.Use(withCORS(cors))
	}
	s.registerRoutes(e)
	sshCtx, sshCancel := context.WithCancel(context.Background())
	if err := s.startSSHGateway(sshCtx); err != nil {
		fmt.Fprintf(os.Stderr, "ssh gateway failed: %v\n", err)
		os.Exit(1)
	}

	addr := ":8080"
	if port := os.Getenv("PORT"); port != "" {
		addr = ":" + port
	}

	srv := &http.Server{Addr: addr, Handler: e}
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-sigs:
		fmt.Fprintf(os.Stdout, "received signal %s, shutting down\n", sig)
		sshCancel()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "server shutdown failed: %v\n", err)
		}
	case err := <-errCh:
		sshCancel()
		fmt.Fprintf(os.Stderr, "server failed: %v\n", err)
		os.Exit(1)
	}
}

func (s *server) registerRoutes(e *echo.Echo) {
	group := e.Group(s.apiPathPrefix())
	group.GET("/healthz", s.handleHealthz)
	internal := group.Group("/internal/v1", s.internalAuthMiddleware())
	if s.internalAuth.enabled {
		internal.GET("/runtime-bindings/:namespace/:instanceId", s.getRuntimeBinding)
		internal.POST("/spritzes", s.createInternalSpritz)
		internal.GET("/spritzes/:namespace/:name", s.getInternalSpritz)
	}
	internal.POST("/debug/chat/send", s.sendInternalDebugChat)
	internal.GET("/shared-mounts/owner/:owner/:mount/latest", s.getSharedMountLatest)
	internal.GET("/shared-mounts/owner/:owner/:mount/revisions/:revision", s.getSharedMountRevision)
	internal.PUT("/shared-mounts/owner/:owner/:mount/revisions/:revision", s.putSharedMountRevision)
	internal.PUT("/shared-mounts/owner/:owner/:mount/latest", s.putSharedMountLatest)
	secured := group.Group("", s.authMiddleware())
	secured.GET("/presets", s.listPresets)
	secured.GET("/spritzes", s.listSpritzes)
	secured.POST("/spritzes/suggest-name", s.suggestSpritzName)
	secured.POST("/channel-routes/resolve", s.resolveChannelRoute)
	secured.POST("/spritzes", s.createSpritz)
	secured.GET("/spritzes/:name", s.getSpritz)
	secured.DELETE("/spritzes/:name", s.deleteSpritz)
	secured.PATCH("/spritzes/:name/user-config", s.updateUserConfig)
	secured.GET("/acp/agents", s.listACPAgents)
	secured.GET("/acp/conversations", s.listACPConversations)
	secured.POST("/acp/conversations", s.createACPConversation)
	secured.GET("/acp/conversations/:id", s.getACPConversation)
	secured.POST("/acp/conversations/:id/bootstrap", s.bootstrapACPConversation)
	secured.PATCH("/acp/conversations/:id", s.updateACPConversation)
	secured.GET("/acp/conversations/:id/connect", s.openACPConversationConnection)
	secured.POST("/spritzes/:name/ssh", s.mintSSHCert)
	if s.terminal.enabled {
		secured.GET("/spritzes/:name/terminal", s.openTerminal)
		secured.GET("/spritzes/:name/terminal/sessions", s.listTerminalSessions)
	}
	if s.instanceProxy.enabled {
		rootSecured := e.Group("", s.authMiddleware())
		prefix := s.instanceProxy.pathPrefix(s.routeModel)
		rootSecured.Any(prefix+"/:name", s.proxyInstanceWeb)
		rootSecured.Any(prefix+"/:name/*", s.proxyInstanceWeb)
	}
}

func (s *server) apiPathPrefix() string {
	prefix := strings.TrimSpace(s.routeModel.APIPathPrefix)
	if prefix == "" {
		return "/api"
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	if len(prefix) > 1 {
		prefix = strings.TrimRight(prefix, "/")
	}
	if prefix == "" {
		return "/api"
	}
	return prefix
}

func (s *server) handleHealthz(c echo.Context) error {
	return c.String(http.StatusOK, "ok")
}

type createRequest struct {
	Name           string              `json:"name"`
	NamePrefix     string              `json:"namePrefix,omitempty"`
	Namespace      string              `json:"namespace,omitempty"`
	PresetID       string              `json:"presetId,omitempty"`
	PresetInputs   json.RawMessage     `json:"presetInputs,omitempty"`
	OwnerID        string              `json:"ownerId,omitempty"`
	OwnerRef       *ownerRef           `json:"ownerRef,omitempty"`
	IdleTTL        string              `json:"idleTtl,omitempty"`
	TTL            string              `json:"ttl,omitempty"`
	IdempotencyKey string              `json:"idempotencyKey,omitempty"`
	Source         string              `json:"source,omitempty"`
	RequestID      string              `json:"requestId,omitempty"`
	Spec           spritzv1.SpritzSpec `json:"spec"`
	UserConfig     json.RawMessage     `json:"userConfig,omitempty"`
	Labels         map[string]string   `json:"labels,omitempty"`
	Annotations    map[string]string   `json:"annotations,omitempty"`
}

type suggestNameRequest struct {
	Namespace  string `json:"namespace,omitempty"`
	Image      string `json:"image,omitempty"`
	PresetID   string `json:"presetId,omitempty"`
	NamePrefix string `json:"namePrefix,omitempty"`
}

func (s *server) resolveSpritzNamespace(requested string) (string, error) {
	namespace := requested
	if s.namespace != "" {
		if namespace != "" && namespace != s.namespace {
			return "", fmt.Errorf("namespace mismatch")
		}
		namespace = s.namespace
	}
	if namespace == "" {
		namespace = "default"
	}
	return namespace, nil
}

func (s *server) namespaceOverrideRequested(requested, resolved string) bool {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return false
	}
	if strings.TrimSpace(s.namespace) == "" {
		return true
	}
	return requested != strings.TrimSpace(resolved)
}

func (s *server) listPresets(c echo.Context) error {
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}
	if principal.isService() && !principal.hasScope(scopePresetsRead) && !principal.isAdminPrincipal() {
		return writeError(c, http.StatusForbidden, "forbidden")
	}
	items := s.presets.public()
	if principal.isService() && !principal.isAdminPrincipal() {
		items = s.presets.publicAllowed(s.provisioners.allowedPresetIDs)
	}
	return writeJSON(c, http.StatusOK, map[string]any{"items": items})
}

func (s *server) suggestSpritzName(c echo.Context) error {
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}

	var body suggestNameRequest
	if err := c.Bind(&body); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid json")
	}
	s.applyProvisionerDefaultSuggestNamePreset(&body, principal)
	metadata, err := s.resolveSuggestNameMetadata(body)
	if err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}

	namespace, err := s.resolveSpritzNamespace(body.Namespace)
	if err != nil {
		return writeError(c, http.StatusForbidden, err.Error())
	}
	requestedNamespace := s.namespaceOverrideRequested(body.Namespace, namespace)
	if principal.isService() {
		if err := s.validateProvisionerPlacement(principal, namespace, metadata.presetID, strings.TrimSpace(body.Image) != "", requestedNamespace, scopeInstancesSuggestName); err != nil {
			if errors.Is(err, errForbidden) {
				return writeError(c, http.StatusForbidden, "forbidden")
			}
			return writeError(c, http.StatusBadRequest, err.Error())
		}
	}
	generator, err := s.newSpritzNameGenerator(c.Request().Context(), namespace, metadata.namePrefix)
	if err != nil {
		return writeError(c, http.StatusInternalServerError, "failed to generate spritz name")
	}
	return writeJSON(c, http.StatusOK, map[string]string{"name": generator()})
}

func (s *server) createSpritz(c echo.Context) error {
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}

	var body createRequest
	if err := c.Bind(&body); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid json")
	}
	normalized, err := s.normalizeCreateRequest(c.Request().Context(), principal, body)
	if err != nil {
		return writeCreateRequestError(c, err)
	}
	body = normalized.body
	namespace := normalized.namespace
	owner := normalized.owner
	var resolvedExternalOwner *externalOwnerResolution
	userConfigKeys := normalized.userConfigKeys
	userConfigPayload := normalized.userConfigPayload
	nameProvided := normalized.nameProvided
	var nameGenerator func() string
	requestedNamePrefix := normalized.requestedNamePrefix
	buildNameGenerator := func(resolved createRequest) error {
		namePrefix := requestedNamePrefix
		if restoredNamePrefix := strings.TrimSpace(resolved.NamePrefix); restoredNamePrefix != "" {
			namePrefix = restoredNamePrefix
		}
		generator, err := s.newSpritzNameGenerator(c.Request().Context(), namespace, s.resolvedCreateNamePrefix(resolved, namePrefix))
		if err != nil {
			return err
		}
		nameGenerator = generator
		return nil
	}
	if !nameProvided {
		if !principal.isService() {
			if err := buildNameGenerator(body); err != nil {
				return writeError(c, http.StatusInternalServerError, "failed to generate spritz name")
			}
			body.Name = nameGenerator()
		}
	}
	if !principal.isService() && body.Name == "" {
		return writeError(c, http.StatusInternalServerError, "failed to generate spritz name")
	}

	provisionerFingerprint := ""
	var provisionerTx *provisionerCreateTransaction
	if principal.isService() {
		provisionerTx = newProvisionerCreateTransaction(
			s,
			c.Request().Context(),
			principal,
			namespace,
			&body,
			normalized.fingerprintRequest,
			normalized.normalizedUserConfig,
			normalized.requestedImage,
			normalized.requestedRepo,
			normalized.requestedNamespace,
		)
		if err := provisionerTx.prepare(); err != nil {
			return writeProvisionerCreateError(c, err)
		}
		owner = body.Spec.Owner
		resolvedExternalOwner = provisionerTx.resolvedExternalOwner
		provisionerFingerprint = provisionerTx.provisionerFingerprint
		if !nameProvided {
			if err := buildNameGenerator(body); err != nil {
				return writeError(c, http.StatusInternalServerError, "failed to generate spritz name")
			}
		}
		existing, err := provisionerTx.replayExisting()
		if err != nil {
			return writeProvisionerCreateError(c, err)
		}
		if existing != nil {
			return writeJSON(c, http.StatusOK, summarizeCreateResponse(existing, principal, body.PresetID, provisionerSource(&body), body.IdempotencyKey, true))
		}
		if err := provisionerTx.finalizeCreate(); err != nil {
			return writeProvisionerCreateError(c, err)
		}
	} else if s.auth.enabled() && !principal.isAdminPrincipal() && owner.ID != principal.ID {
		return writeError(c, http.StatusForbidden, "owner mismatch")
	}

	if !principal.isService() {
		if err := s.resolveCreateAdmission(c.Request().Context(), principal, namespace, &body); err != nil {
			var admissionErr *admissionError
			if errors.As(err, &admissionErr) {
				if admissionErr.data != nil {
					return writeJSendFailData(c, admissionErr.status, admissionErr.data)
				}
				return writeError(c, admissionErr.status, admissionErr.message)
			}
			return writeError(c, http.StatusInternalServerError, err.Error())
		}
		if err := resolveCreateLifetimes(&body.Spec, s.provisioners, false); err != nil {
			return writeError(c, http.StatusBadRequest, err.Error())
		}
	}
	if err := s.ensureServiceAccount(c.Request().Context(), namespace, body.Spec.ServiceAccountName); err != nil {
		return writeError(c, http.StatusInternalServerError, "failed to ensure service account")
	}

	labels := map[string]string{
		ownerLabelKey: ownerLabelValue(owner.ID),
	}
	if principal.isService() {
		labels[actorLabelKey] = actorLabelValue(principal.ID)
		labels[idempotencyLabelKey] = idempotencyLabelValue(body.IdempotencyKey)
	}
	if body.PresetID != "" {
		labels[presetLabelKey] = body.PresetID
	}
	for k, v := range body.Labels {
		labels[k] = v
	}
	annotations := mergeStringMap(s.defaultMetadata, body.Annotations)
	if len(userConfigKeys) > 0 {
		encoded, err := encodeUserConfig(userConfigKeys, userConfigPayload)
		if err != nil {
			return writeError(c, http.StatusBadRequest, "invalid userConfig")
		}
		if encoded != "" {
			annotations = mergeStringMap(annotations, map[string]string{
				userConfigAnnotationKey: encoded,
			})
		}
	}
	if body.PresetID != "" {
		annotations = mergeStringMap(annotations, map[string]string{
			presetIDAnnotationKey: body.PresetID,
		})
	}
	if resolvedExternalOwner != nil {
		externalOwnerAnnotations := map[string]string{
			externalOwnerIssuerAnnotationKey:      resolvedExternalOwner.Issuer,
			externalOwnerProviderAnnotationKey:    resolvedExternalOwner.Provider,
			externalOwnerSubjectHashAnnotationKey: resolvedExternalOwner.SubjectHash,
			externalOwnerResolvedAtAnnotationKey:  resolvedExternalOwner.ResolvedAt.Format(time.RFC3339),
		}
		if strings.TrimSpace(resolvedExternalOwner.Tenant) != "" {
			externalOwnerAnnotations[externalOwnerTenantAnnotationKey] = resolvedExternalOwner.Tenant
		}
		annotations = mergeStringMap(annotations, externalOwnerAnnotations)
	}

	applySSHDefaults(&body.Spec, s.sshDefaults, namespace)
	baseSpec := body.Spec

	createSpritzResource := func(name string) (*spritzv1.Spritz, error) {
		var spec spritzv1.SpritzSpec
		baseSpec.DeepCopyInto(&spec)
		applyIngressDefaults(&spec, name, namespace, s.ingressDefaults)
		if spec.Ingress != nil && strings.EqualFold(spec.Ingress.Mode, "gateway") && spec.Ingress.Host == "" {
			return nil, fmt.Errorf("spec.ingress.host is required when spec.ingress.mode=gateway")
		}
		if spec.Ingress != nil && strings.EqualFold(spec.Ingress.Mode, "gateway") && spec.Ingress.GatewayName == "" {
			return nil, fmt.Errorf("spec.ingress.gatewayName is required when spec.ingress.mode=gateway")
		}

		resourceLabels := map[string]string{}
		for k, v := range labels {
			resourceLabels[k] = v
		}
		resourceLabels[nameLabelKey] = name

		return &spritzv1.Spritz{
			TypeMeta: metav1.TypeMeta{Kind: "Spritz", APIVersion: spritzv1.GroupVersion.String()},
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   namespace,
				Labels:      resourceLabels,
				Annotations: annotations,
			},
			Spec: spec,
		}, nil
	}

	attempts := 1
	if !nameProvided {
		attempts = 8
	}
	currentName := body.Name
	for attempt := 0; attempt < attempts; attempt++ {
		name := currentName
		failedName := ""
		if !nameProvided {
			if attempt > 0 {
				failedName = currentName
			}
			if strings.TrimSpace(name) == "" || attempt > 0 {
				name = nameGenerator()
			}
		}
		if principal.isService() {
			reservedName, replayed, err := provisionerTx.reserveAttemptName(failedName, name)
			if err != nil {
				return writeProvisionerCreateError(c, err)
			}
			name = reservedName
			if replayed != nil {
				return writeJSON(c, http.StatusOK, summarizeCreateResponse(replayed, principal, body.PresetID, provisionerSource(&body), body.IdempotencyKey, true))
			}
		}
		spritz, err := createSpritzResource(name)
		if err != nil {
			return writeError(c, http.StatusBadRequest, err.Error())
		}
		if err := s.client.Create(c.Request().Context(), spritz); err != nil {
			if principal.isService() && apierrors.IsAlreadyExists(err) {
				existing, getErr := s.findReservedSpritz(c.Request().Context(), namespace, name)
				if getErr != nil {
					return writeError(c, http.StatusInternalServerError, getErr.Error())
				}
				if matchesIdempotentReplayTarget(existing, principal, body.IdempotencyKey, provisionerFingerprint) {
					return writeJSON(c, http.StatusOK, summarizeCreateResponse(existing, principal, body.PresetID, provisionerSource(&body), body.IdempotencyKey, true))
				}
				if !nameProvided {
					currentName = name
					continue
				}
				return writeError(c, http.StatusConflict, "idempotencyKey already used with a different request")
			}
			if !nameProvided && apierrors.IsAlreadyExists(err) {
				continue
			}
			return writeError(c, http.StatusInternalServerError, err.Error())
		}
		if principal.isService() {
			if err := s.completeIdempotencyReservation(c.Request().Context(), principal.ID, body.IdempotencyKey, spritz); err != nil {
				return writeError(c, http.StatusInternalServerError, err.Error())
			}
		}
		return writeJSON(c, http.StatusCreated, summarizeCreateResponse(spritz, principal, body.PresetID, provisionerSource(&body), body.IdempotencyKey, false))
	}

	return writeError(c, http.StatusInternalServerError, "failed to generate unique spritz name")
}

// ensureServiceAccount creates the requested instance service account on demand.
func (s *server) ensureServiceAccount(ctx context.Context, namespace, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}

	objectKey := client.ObjectKey{Namespace: namespace, Name: name}
	serviceAccount := &corev1.ServiceAccount{}
	if err := s.client.Get(ctx, objectKey, serviceAccount); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}

	serviceAccount = &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	if err := s.client.Create(ctx, serviceAccount); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (s *server) listSpritzes(c echo.Context) error {
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}
	if err := authorizeHumanOnly(principal, s.auth.enabled()); err != nil {
		return writeForbidden(c)
	}

	namespace := s.namespace
	if namespace == "" {
		namespace = c.QueryParam("namespace")
	}

	list := &spritzv1.SpritzList{}
	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}

	if err := s.client.List(c.Request().Context(), list, opts...); err != nil {
		return writeError(c, http.StatusInternalServerError, err.Error())
	}

	if s.auth.enabled() {
		filtered := make([]spritzv1.Spritz, 0, len(list.Items))
		for _, item := range list.Items {
			if err := authorizeHumanOwnedAccess(principal, item.Spec.Owner.ID, true); err == nil {
				filtered = append(filtered, item)
			}
		}
		list.Items = filtered
	}

	return writeJSON(c, http.StatusOK, list)
}

func (s *server) getSpritz(c echo.Context) error {
	name := c.Param("name")
	if name == "" {
		return writeError(c, http.StatusNotFound, "not found")
	}
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}
	if err := authorizeHumanOnly(principal, s.auth.enabled()); err != nil {
		return writeForbidden(c)
	}

	namespace := s.namespace
	if namespace == "" {
		namespace = c.QueryParam("namespace")
	}
	if namespace == "" {
		namespace = "default"
	}

	spritz := &spritzv1.Spritz{}
	if err := s.client.Get(c.Request().Context(), client.ObjectKey{Name: name, Namespace: namespace}, spritz); err != nil {
		return writeError(c, http.StatusNotFound, err.Error())
	}
	if err := authorizeHumanOwnedAccess(principal, spritz.Spec.Owner.ID, s.auth.enabled()); err != nil {
		return writeError(c, http.StatusForbidden, "forbidden")
	}

	return writeJSON(c, http.StatusOK, spritz)
}

func (s *server) updateUserConfig(c echo.Context) error {
	name := c.Param("name")
	if name == "" {
		return writeError(c, http.StatusNotFound, "not found")
	}
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}
	if err := authorizeHumanOnly(principal, s.auth.enabled()); err != nil {
		return writeForbidden(c)
	}

	namespace := s.namespace
	if namespace == "" {
		namespace = c.QueryParam("namespace")
	}
	if namespace == "" {
		namespace = "default"
	}

	spritz := &spritzv1.Spritz{}
	if err := s.client.Get(c.Request().Context(), client.ObjectKey{Name: name, Namespace: namespace}, spritz); err != nil {
		return writeError(c, http.StatusNotFound, err.Error())
	}
	if err := authorizeHumanOwnedAccess(principal, spritz.Spec.Owner.ID, s.auth.enabled()); err != nil {
		return writeError(c, http.StatusForbidden, "forbidden")
	}

	raw, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return writeError(c, http.StatusBadRequest, "invalid userConfig")
	}
	userConfigKeys, userConfigPayload, err := parseUserConfig(raw)
	if err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	if len(userConfigKeys) == 0 {
		return writeError(c, http.StatusBadRequest, "userConfig is required")
	}
	normalized, err := normalizeUserConfig(s.userConfigPolicy, userConfigKeys, userConfigPayload)
	if err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	applyUserConfig(&spritz.Spec, userConfigKeys, normalized)

	if spritz.Spec.Repo != nil && len(spritz.Spec.Repos) > 0 {
		return writeError(c, http.StatusBadRequest, "spec.repo cannot be set when spec.repos is provided")
	}
	if spritz.Spec.Repo != nil {
		if err := validateRepoDir(spritz.Spec.Repo.Dir); err != nil {
			return writeError(c, http.StatusBadRequest, err.Error())
		}
	}
	for _, repo := range spritz.Spec.Repos {
		if err := validateRepoDir(repo.Dir); err != nil {
			return writeError(c, http.StatusBadRequest, err.Error())
		}
	}
	if len(spritz.Spec.SharedMounts) > 0 {
		normalizedMounts, err := normalizeSharedMounts(spritz.Spec.SharedMounts)
		if err != nil {
			return writeError(c, http.StatusBadRequest, err.Error())
		}
		spritz.Spec.SharedMounts = normalizedMounts
	}

	annotations := spritz.Annotations
	encoded, err := encodeUserConfig(userConfigKeys, normalized)
	if err != nil {
		return writeError(c, http.StatusBadRequest, "invalid userConfig")
	}
	if encoded == "" {
		if annotations != nil {
			delete(annotations, userConfigAnnotationKey)
		}
	} else {
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[userConfigAnnotationKey] = encoded
	}
	spritz.Annotations = annotations

	if err := s.client.Update(c.Request().Context(), spritz); err != nil {
		return writeError(c, http.StatusInternalServerError, err.Error())
	}

	return writeJSON(c, http.StatusOK, spritz)
}

func (s *server) deleteSpritz(c echo.Context) error {
	name := c.Param("name")
	if name == "" {
		return writeError(c, http.StatusNotFound, "not found")
	}
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}
	if err := authorizeHumanOnly(principal, s.auth.enabled()); err != nil {
		return writeForbidden(c)
	}

	namespace := s.namespace
	if namespace == "" {
		namespace = c.QueryParam("namespace")
	}
	if namespace == "" {
		namespace = "default"
	}

	spritz := &spritzv1.Spritz{}
	if err := s.client.Get(c.Request().Context(), client.ObjectKey{Name: name, Namespace: namespace}, spritz); err != nil {
		return writeError(c, http.StatusNotFound, err.Error())
	}
	if err := authorizeHumanOwnedAccess(principal, spritz.Spec.Owner.ID, s.auth.enabled()); err != nil {
		return writeError(c, http.StatusForbidden, "forbidden")
	}

	if err := s.client.Delete(c.Request().Context(), spritz); err != nil {
		return writeError(c, http.StatusInternalServerError, err.Error())
	}

	return c.NoContent(http.StatusNoContent)
}

func validateRepoDir(dir string) error {
	if dir == "" {
		return nil
	}
	cleaned := path.Clean(dir)
	if strings.HasPrefix(cleaned, "..") {
		return fmt.Errorf("spec.repo.dir must not escape /workspace")
	}
	if strings.HasPrefix(cleaned, "/") {
		if !strings.HasPrefix(cleaned, "/workspace/") && cleaned != "/workspace" {
			return fmt.Errorf("spec.repo.dir must be under /workspace")
		}
	}
	return nil
}

func writeJSON(c echo.Context, status int, payload any) error {
	return writeJSendSuccess(c, status, payload)
}
