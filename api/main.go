package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	client            client.Client
	clientset         *kubernetes.Clientset
	restConfig        *rest.Config
	scheme            *runtime.Scheme
	namespace         string
	auth              authConfig
	internalAuth      internalAuthConfig
	ingressDefaults   ingressDefaults
	terminal          terminalConfig
	sshGateway        sshGatewayConfig
	sshDefaults       sshDefaults
	sshMintLimiter    *sshMintLimiter
	defaultMetadata   map[string]string
	sharedMounts      sharedMountsConfig
	sharedMountsStore *sharedMountsStore
	userConfigPolicy  userConfigPolicy
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
	ingressDefaults := newIngressDefaults()
	terminal := newTerminalConfig()
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
		auth:              auth,
		internalAuth:      internalAuth,
		ingressDefaults:   ingressDefaults,
		terminal:          terminal,
		sshGateway:        sshGateway,
		sshDefaults:       sshDefaults,
		sshMintLimiter:    sshMintLimiter,
		defaultMetadata:   defaultAnnotations,
		sharedMounts:      sharedMounts,
		sharedMountsStore: sharedStore,
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
	e.GET("/healthz", s.handleHealthz)
	internal := e.Group("/internal/v1", s.internalAuthMiddleware())
	internal.GET("/shared-mounts/owner/:owner/:mount/latest", s.getSharedMountLatest)
	internal.GET("/shared-mounts/owner/:owner/:mount/revisions/:revision", s.getSharedMountRevision)
	internal.PUT("/shared-mounts/owner/:owner/:mount/revisions/:revision", s.putSharedMountRevision)
	internal.PUT("/shared-mounts/owner/:owner/:mount/latest", s.putSharedMountLatest)
	secured := e.Group("", s.authMiddleware())
	secured.GET("/spritzes", s.listSpritzes)
	secured.POST("/spritzes", s.createSpritz)
	secured.GET("/spritzes/:name", s.getSpritz)
	secured.DELETE("/spritzes/:name", s.deleteSpritz)
	secured.PATCH("/spritzes/:name/user-config", s.updateUserConfig)
	secured.POST("/spritzes/:name/ssh", s.mintSSHCert)
	if s.terminal.enabled {
		secured.GET("/spritzes/:name/terminal", s.openTerminal)
		secured.GET("/spritzes/:name/terminal/sessions", s.listTerminalSessions)
	}
}

func (s *server) handleHealthz(c echo.Context) error {
	return c.String(http.StatusOK, "ok")
}

type createRequest struct {
	Name        string              `json:"name"`
	Namespace   string              `json:"namespace,omitempty"`
	Spec        spritzv1.SpritzSpec `json:"spec"`
	UserConfig  json.RawMessage     `json:"userConfig,omitempty"`
	Labels      map[string]string   `json:"labels,omitempty"`
	Annotations map[string]string   `json:"annotations,omitempty"`
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
	body.Name = strings.TrimSpace(body.Name)

	userConfigKeys, userConfigPayload, err := parseUserConfig(body.UserConfig)
	if err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	if len(userConfigKeys) > 0 {
		normalized, err := normalizeUserConfig(s.userConfigPolicy, userConfigKeys, userConfigPayload)
		if err != nil {
			return writeError(c, http.StatusBadRequest, err.Error())
		}
		userConfigPayload = normalized
		applyUserConfig(&body.Spec, userConfigKeys, userConfigPayload)
	}

	if body.Spec.Image == "" {
		return writeError(c, http.StatusBadRequest, "spec.image is required")
	}
	if body.Spec.Repo != nil && len(body.Spec.Repos) > 0 {
		return writeError(c, http.StatusBadRequest, "spec.repo cannot be set when spec.repos is provided")
	}
	if body.Spec.Repo != nil {
		if err := validateRepoDir(body.Spec.Repo.Dir); err != nil {
			return writeError(c, http.StatusBadRequest, err.Error())
		}
	}
	for _, repo := range body.Spec.Repos {
		if err := validateRepoDir(repo.Dir); err != nil {
			return writeError(c, http.StatusBadRequest, err.Error())
		}
	}
	if len(body.Spec.SharedMounts) > 0 {
		normalized, err := normalizeSharedMounts(body.Spec.SharedMounts)
		if err != nil {
			return writeError(c, http.StatusBadRequest, err.Error())
		}
		body.Spec.SharedMounts = normalized
	}

	namespace := body.Namespace
	if s.namespace != "" {
		if namespace != "" && namespace != s.namespace {
			return writeError(c, http.StatusForbidden, "namespace mismatch")
		}
		namespace = s.namespace
	}
	if namespace == "" {
		namespace = "default"
	}

	nameProvided := body.Name != ""
	var nameGenerator func() string
	if !nameProvided {
		generator, err := s.newSpritzNameGenerator(c.Request().Context(), namespace)
		if err != nil {
			return writeError(c, http.StatusInternalServerError, "failed to generate spritz name")
		}
		nameGenerator = generator
		body.Name = nameGenerator()
	}
	if body.Name == "" {
		return writeError(c, http.StatusInternalServerError, "failed to generate spritz name")
	}

	owner := body.Spec.Owner
	if owner.ID == "" {
		if s.auth.enabled() {
			owner.ID = principal.ID
		} else {
			return writeError(c, http.StatusBadRequest, "spec.owner.id is required")
		}
	}
	if owner.Email == "" {
		owner.Email = principal.Email
	}
	if s.auth.enabled() && !principal.IsAdmin && owner.ID != principal.ID {
		return writeError(c, http.StatusForbidden, "owner mismatch")
	}

	labels := map[string]string{
		ownerLabelKey: ownerLabelValue(owner.ID),
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

	body.Spec.Owner = owner
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
	for attempt := 0; attempt < attempts; attempt++ {
		name := body.Name
		if !nameProvided && attempt > 0 {
			name = nameGenerator()
		}
		spritz, err := createSpritzResource(name)
		if err != nil {
			return writeError(c, http.StatusBadRequest, err.Error())
		}
		if err := s.client.Create(c.Request().Context(), spritz); err != nil {
			if !nameProvided && apierrors.IsAlreadyExists(err) {
				continue
			}
			return writeError(c, http.StatusInternalServerError, err.Error())
		}
		return writeJSON(c, http.StatusCreated, spritz)
	}

	return writeError(c, http.StatusInternalServerError, "failed to generate unique spritz name")
}

func (s *server) listSpritzes(c echo.Context) error {
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
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
	if s.auth.enabled() && !principal.IsAdmin {
		opts = append(opts, client.MatchingLabels{ownerLabelKey: ownerLabelValue(principal.ID)})
	}

	if err := s.client.List(c.Request().Context(), list, opts...); err != nil {
		return writeError(c, http.StatusInternalServerError, err.Error())
	}

	if s.auth.enabled() && !principal.IsAdmin {
		filtered := make([]spritzv1.Spritz, 0, len(list.Items))
		for _, item := range list.Items {
			if item.Spec.Owner.ID == principal.ID {
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
	if s.auth.enabled() && !principal.IsAdmin && spritz.Spec.Owner.ID != principal.ID {
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
	if s.auth.enabled() && !principal.IsAdmin && spritz.Spec.Owner.ID != principal.ID {
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
	if s.auth.enabled() && !principal.IsAdmin && spritz.Spec.Owner.ID != principal.ID {
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
