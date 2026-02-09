package controllers

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	spritzv1 "spritz.sh/operator/api/v1"
)

const (
	defaultRepoDir                 = "/workspace/repo"
	defaultWebPort                 = int32(8080)
	defaultSSHPort                 = int32(22)
	defaultSSHUser                 = "spritz"
	defaultSSHMode                 = "service"
	spritzContainerName            = "spritz"
	spritzFinalizer                = "spritz.sh/finalizer"
	defaultTTLGrace                = 5 * time.Minute
	defaultRepoInitImage           = "alpine/git:2.45.2"
	repoAuthMountPath              = "/var/run/spritz/repo-auth"
	repoInitHomeDir                = "/home/dev"
	repoInitGroupID          int64 = 65532
	defaultSharedConfigMount       = "/shared"
)

var (
	defaultWorkspaceSizeLimit = resource.MustParse("10Gi")
	defaultHomeSizeLimit      = resource.MustParse("5Gi")
	defaultSharedConfigSize   = resource.MustParse("1Gi")
)

type homePVCSettings struct {
	enabled      bool
	prefix       string
	size         resource.Quantity
	accessModes  []corev1.PersistentVolumeAccessMode
	storageClass string
	mountPaths   []string
}

type sharedConfigPVCSettings struct {
	enabled      bool
	prefix       string
	size         resource.Quantity
	accessModes  []corev1.PersistentVolumeAccessMode
	storageClass string
	mountPath    string
}

type SpritzReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

type repoEntry struct {
	repo spritzv1.SpritzRepo
	dir  string
}

func primaryRepo(spritz *spritzv1.Spritz) (*spritzv1.SpritzRepo, bool) {
	if spritz.Spec.Repo != nil && strings.TrimSpace(spritz.Spec.Repo.URL) != "" && len(spritz.Spec.Repos) == 0 {
		return spritz.Spec.Repo, true
	}
	if len(spritz.Spec.Repos) > 0 {
		return &spritz.Spec.Repos[0], true
	}
	if spritz.Spec.Repo != nil && strings.TrimSpace(spritz.Spec.Repo.URL) != "" {
		return spritz.Spec.Repo, true
	}
	return nil, false
}

func repoEntries(spritz *spritzv1.Spritz) []spritzv1.SpritzRepo {
	if len(spritz.Spec.Repos) > 0 {
		return spritz.Spec.Repos
	}
	if spritz.Spec.Repo != nil && strings.TrimSpace(spritz.Spec.Repo.URL) != "" {
		return []spritzv1.SpritzRepo{*spritz.Spec.Repo}
	}
	return nil
}

func repoDirFor(repo spritzv1.SpritzRepo, index int, total int) string {
	repoDir := repo.Dir
	if repoDir == "" {
		if total > 1 {
			repoDir = fmt.Sprintf("/workspace/repo-%d", index+1)
		} else if inferred := inferRepoName(repo.URL); inferred != "" {
			repoDir = path.Join("/workspace", inferred)
		} else {
			repoDir = defaultRepoDir
		}
	}
	if !strings.HasPrefix(repoDir, "/") {
		repoDir = path.Join("/workspace", repoDir)
	}
	return path.Clean(repoDir)
}

func inferRepoName(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	pathPart := ""
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return ""
		}
		pathPart = parsed.Path
	} else if strings.Contains(value, ":") {
		parts := strings.SplitN(value, ":", 2)
		if len(parts) == 2 {
			pathPart = parts[1]
		} else {
			pathPart = value
		}
	} else {
		pathPart = value
	}
	pathPart = strings.SplitN(pathPart, "?", 2)[0]
	pathPart = strings.SplitN(pathPart, "#", 2)[0]
	pathPart = strings.TrimSuffix(pathPart, "/")
	if pathPart == "" {
		return ""
	}
	base := path.Base(pathPart)
	if base == "." || base == "/" {
		return ""
	}
	base = strings.TrimSuffix(base, ".git")
	if base == "" || base == "." || base == "/" {
		return ""
	}
	return base
}

func validateRepoDir(repoDir string) error {
	if repoDir == "" {
		return nil
	}
	cleaned := path.Clean(repoDir)
	if path.IsAbs(cleaned) {
		if !pathHasPrefix(cleaned, "/workspace") {
			return fmt.Errorf("repo.dir must be under /workspace")
		}
		return nil
	}
	if strings.HasPrefix(cleaned, "..") {
		return fmt.Errorf("repo.dir must not escape /workspace")
	}
	return nil
}

func (r *SpritzReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	logger := log.FromContext(ctx)

	var spritz spritzv1.Spritz
	if err := r.Get(ctx, req.NamespacedName, &spritz); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if done, err := r.reconcileLifecycle(ctx, &spritz); done || err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileResources(ctx, &spritz); err != nil {
		return ctrl.Result{}, err
	}

	requeueAfter, err := r.reconcileStatus(ctx, &spritz)
	if err != nil {
		return ctrl.Result{}, err
	}
	if requeueAfter != nil {
		return ctrl.Result{RequeueAfter: *requeueAfter}, nil
	}

	logger.V(1).Info("spritz reconciled")
	return ctrl.Result{}, nil
}

func (r *SpritzReconciler) reconcileLifecycle(ctx context.Context, spritz *spritzv1.Spritz) (bool, error) {
	logger := log.FromContext(ctx)
	if !spritz.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(spritz, spritzFinalizer) {
			if err := r.setStatus(ctx, spritz, "Terminating", "", buildSSHInfo(spritz), "Deleting", "spritz deletion requested"); err != nil {
				logger.Error(err, "failed to set terminating status")
			}
			controllerutil.RemoveFinalizer(spritz, spritzFinalizer)
			if err := r.Update(ctx, spritz); err != nil {
				return true, err
			}
		}
		return true, nil
	}

	if !controllerutil.ContainsFinalizer(spritz, spritzFinalizer) {
		controllerutil.AddFinalizer(spritz, spritzFinalizer)
		if err := r.Update(ctx, spritz); err != nil {
			return true, err
		}
		return true, nil
	}

	return false, nil
}

func (r *SpritzReconciler) reconcileResources(ctx context.Context, spritz *spritzv1.Spritz) error {
	if err := r.reconcileDeployment(ctx, spritz); err != nil {
		return err
	}
	if err := r.reconcileService(ctx, spritz); err != nil {
		return err
	}
	if err := r.reconcileIngress(ctx, spritz); err != nil {
		return err
	}
	if err := r.reconcileGatewayRoute(ctx, spritz); err != nil {
		return err
	}
	return nil
}

func (r *SpritzReconciler) reconcileDeployment(ctx context.Context, spritz *spritzv1.Spritz) error {
	labels := baseLabels(spritz)
	workspaceSizeLimit := emptyDirSizeLimit("SPRITZ_WORKSPACE_SIZE_LIMIT", defaultWorkspaceSizeLimit)
	homeSizeLimit := emptyDirSizeLimit("SPRITZ_HOME_SIZE_LIMIT", defaultHomeSizeLimit)

	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: spritz.Name, Namespace: spritz.Namespace}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		if err := controllerutil.SetControllerReference(spritz, deploy, r.Scheme); err != nil {
			return err
		}

		deploy.Labels = mergeMaps(labels, spritz.Spec.Labels)
		deploy.Annotations = mergeMaps(deploy.Annotations, spritz.Spec.Annotations)
		deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		deploy.Spec.Template.Labels = labels
		deploy.Spec.Template.Annotations = mergeMaps(deploy.Spec.Template.Annotations, spritz.Spec.Annotations)

		repos := repoEntries(spritz)
		for _, repo := range repos {
			if err := validateRepoDir(repo.Dir); err != nil {
				return err
			}
		}
		var repoDirs []string
		for i, repo := range repos {
			if strings.TrimSpace(repo.URL) == "" {
				continue
			}
			repoDirs = append(repoDirs, repoDirFor(repo, i, len(repos)))
		}

		env := []corev1.EnvVar{}
		primary, hasPrimary := primaryRepo(spritz)
		if hasPrimary {
			primaryDir := defaultRepoDir
			if len(repos) > 0 {
				primaryDir = repoDirFor(repos[0], 0, len(repos))
			} else {
				primaryDir = repoDirFor(*primary, 0, 1)
			}
			env = append(env, corev1.EnvVar{Name: "SPRITZ_REPO_DIR", Value: primaryDir})
			if primary.URL != "" {
				env = append(env, corev1.EnvVar{Name: "SPRITZ_REPO_URL", Value: primary.URL})
			}
			if primary.Branch != "" {
				env = append(env, corev1.EnvVar{Name: "SPRITZ_REPO_BRANCH", Value: primary.Branch})
			}
			if primary.Revision != "" {
				env = append(env, corev1.EnvVar{Name: "SPRITZ_REPO_REVISION", Value: primary.Revision})
			}
			if primary.Depth > 0 {
				env = append(env, corev1.EnvVar{Name: "SPRITZ_REPO_DEPTH", Value: fmt.Sprintf("%d", primary.Depth)})
			}
			if primary.Submodules {
				env = append(env, corev1.EnvVar{Name: "SPRITZ_REPO_SUBMODULES", Value: "true"})
			}
		}
		env = append(env, spritz.Spec.Env...)

		ports := containerPorts(spritz)
		homeSettings := loadHomePVCSettings()
		if err := validateMountPaths(homeSettings.mountPaths); err != nil {
			return err
		}
		sharedSettings := loadSharedConfigPVCSettings()
		if sharedSettings.enabled {
			if err := validateSharedConfigMountPath(sharedSettings.mountPath); err != nil {
				return err
			}
		}
		sharedMountsSettings, err := loadSharedMountsSettings()
		if err != nil {
			return err
		}
		homeVolumeSource := corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: homeSizeLimit},
		}
		homePVCEnabled := homeSettings.enabled && spritz.Spec.Owner.ID != ""
		if homePVCEnabled {
			homePVCName := ownerPVCName(homeSettings.prefix, spritz.Spec.Owner.ID)
			if err := r.ensureHomePVC(ctx, spritz, homePVCName, homeSettings); err != nil {
				return fmt.Errorf("failed to ensure home PVC %s: %w", homePVCName, err)
			}
			homeVolumeSource = corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: homePVCName,
				},
			}
		}
		sharedPVCEnabled := sharedSettings.enabled && spritz.Spec.Owner.ID != ""
		sharedVolumeSource := corev1.VolumeSource{}
		if sharedPVCEnabled {
			sharedPVCName := ownerPVCName(sharedSettings.prefix, spritz.Spec.Owner.ID)
			if err := r.ensureSharedConfigPVC(ctx, spritz, sharedPVCName, sharedSettings); err != nil {
				return fmt.Errorf("failed to ensure shared config PVC %s: %w", sharedPVCName, err)
			}
			sharedVolumeSource = corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: sharedPVCName,
				},
			}
		}
		nodeSelector, err := loadPodNodeSelector()
		if err != nil {
			return err
		}
		homeMounts := buildHomeMounts(homeSettings)
		sharedMountRuntime, err := buildSharedMountRuntime(spritz, sharedMountsSettings)
		if err != nil {
			return err
		}
		repoMountRoots := append([]corev1.VolumeMount{}, homeMounts...)
		if sharedPVCEnabled {
			repoMountRoots = append(repoMountRoots, corev1.VolumeMount{Name: "shared-config", MountPath: sharedSettings.mountPath})
		}
		if len(sharedMountRuntime.volumeMounts) > 0 {
			repoMountRoots = append(repoMountRoots, sharedMountRuntime.volumeMounts...)
		}
		repoInitContainers, repoAuthVolumes, err := buildRepoInitContainers(spritz, repos, repoMountRoots)
		if err != nil {
			return err
		}

		volumes := []corev1.Volume{
			{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: workspaceSizeLimit}}},
			{Name: "home", VolumeSource: homeVolumeSource},
		}
		if sharedPVCEnabled {
			volumes = append(volumes, corev1.Volume{Name: "shared-config", VolumeSource: sharedVolumeSource})
		}
		if len(repoAuthVolumes) > 0 {
			volumes = append(volumes, repoAuthVolumes...)
		}

		volumeMounts := append([]corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}}, homeMounts...)
		if sharedPVCEnabled {
			volumeMounts = append(volumeMounts, corev1.VolumeMount{Name: "shared-config", MountPath: sharedSettings.mountPath})
		}
		if len(sharedMountRuntime.volumes) > 0 {
			volumes = append(volumes, sharedMountRuntime.volumes...)
		}
		if len(sharedMountRuntime.volumeMounts) > 0 {
			volumeMounts = append(volumeMounts, sharedMountRuntime.volumeMounts...)
		}
		if len(sharedMountRuntime.env) > 0 {
			env = append(env, sharedMountRuntime.env...)
		}
		volumeMounts = appendRepoDirMounts(volumeMounts, repoDirs, repoMountRoots)
		podSpec := corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:         spritzContainerName,
					Image:        spritz.Spec.Image,
					Env:          env,
					Resources:    spritz.Spec.Resources,
					Ports:        ports,
					VolumeMounts: volumeMounts,
				},
			},
			Volumes: volumes,
		}
		podSpec.SecurityContext = buildPodSecurityContext(homePVCEnabled, sharedPVCEnabled, sharedMountsSettings.enabled, len(repoInitContainers) > 0)
		initContainers := []corev1.Container{}
		if sharedMountRuntime.initContainer != nil {
			initContainers = append(initContainers, *sharedMountRuntime.initContainer)
		}
		if len(repoInitContainers) > 0 {
			initContainers = append(initContainers, repoInitContainers...)
		}
		if len(initContainers) > 0 {
			podSpec.InitContainers = initContainers
		}
		if sharedMountRuntime.sidecarContainer != nil {
			podSpec.Containers = append(podSpec.Containers, *sharedMountRuntime.sidecarContainer)
		}
		if len(nodeSelector) > 0 {
			podSpec.NodeSelector = nodeSelector
		}
		deploy.Spec.Template.Spec = podSpec
		return nil
	})

	return err
}

func (r *SpritzReconciler) reconcileService(ctx context.Context, spritz *spritzv1.Spritz) error {
	if len(spritz.Spec.Ports) == 0 && !isWebEnabled(spritz) && !shouldExposeSSHService(spritz) {
		svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: spritz.Name, Namespace: spritz.Namespace}}
		if err := r.Delete(ctx, svc); err != nil && !errors.IsNotFound(err) {
			return err
		}
		return nil
	}

	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: spritz.Name, Namespace: spritz.Namespace}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if err := controllerutil.SetControllerReference(spritz, svc, r.Scheme); err != nil {
			return err
		}

		labels := baseLabels(spritz)
		svc.Labels = mergeMaps(labels, spritz.Spec.Labels)
		svc.Spec.Selector = labels
		svc.Annotations = mergeMaps(svc.Annotations, spritz.Spec.Annotations)

		svc.Spec.Ports = servicePorts(spritz)
		return nil
	})

	return err
}

func (r *SpritzReconciler) reconcileIngress(ctx context.Context, spritz *spritzv1.Spritz) error {
	if !shouldUseIngress(spritz) {
		ing := &netv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: spritz.Name, Namespace: spritz.Namespace}}
		if err := r.Delete(ctx, ing); err != nil && !errors.IsNotFound(err) {
			return err
		}
		return nil
	}

	ing := &netv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: spritz.Name, Namespace: spritz.Namespace}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ing, func() error {
		if err := controllerutil.SetControllerReference(spritz, ing, r.Scheme); err != nil {
			return err
		}

		labels := baseLabels(spritz)
		ing.Labels = mergeMaps(labels, spritz.Spec.Labels)
		ing.Annotations = mergeMaps(ing.Annotations, spritz.Spec.Annotations)
		ing.Annotations = mergeMaps(ing.Annotations, spritz.Spec.Ingress.Annotations)

		if spritz.Spec.Ingress.ClassName != "" {
			ing.Spec.IngressClassName = &spritz.Spec.Ingress.ClassName
		}

		path := spritz.Spec.Ingress.Path
		if path == "" {
			path = "/"
		}

		ing.Spec.Rules = []netv1.IngressRule{
			{
				Host: spritz.Spec.Ingress.Host,
				IngressRuleValue: netv1.IngressRuleValue{
					HTTP: &netv1.HTTPIngressRuleValue{
						Paths: []netv1.HTTPIngressPath{
							{
								Path:     path,
								PathType: pathTypePtr(netv1.PathTypePrefix),
								Backend: netv1.IngressBackend{
									Service: &netv1.IngressServiceBackend{
										Name: spritz.Name,
										Port: netv1.ServiceBackendPort{Name: httpPortName(spritz)},
									},
								},
							},
						},
					},
				},
			},
		}

		return nil
	})

	return err
}

func (r *SpritzReconciler) reconcileGatewayRoute(ctx context.Context, spritz *spritzv1.Spritz) error {
	if !shouldUseGatewayRoute(spritz) {
		route := &gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: spritz.Name, Namespace: spritz.Namespace}}
		if err := r.Delete(ctx, route); err != nil && !errors.IsNotFound(err) {
			return err
		}
		return nil
	}

	logger := log.FromContext(ctx)
	if spritz.Spec.Ingress.GatewayName == "" {
		logger.Info("skipping HTTPRoute; ingress.gatewayName is required for gateway mode", "name", spritz.Name, "namespace", spritz.Namespace)
		return nil
	}
	route := &gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: spritz.Name, Namespace: spritz.Namespace}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, route, func() error {
		if err := controllerutil.SetControllerReference(spritz, route, r.Scheme); err != nil {
			return err
		}

		labels := baseLabels(spritz)
		route.Labels = mergeMaps(labels, spritz.Spec.Labels)
		route.Annotations = mergeMaps(route.Annotations, spritz.Spec.Annotations)
		route.Annotations = mergeMaps(route.Annotations, spritz.Spec.Ingress.Annotations)

		path := spritz.Spec.Ingress.Path
		if path == "" {
			path = "/"
		}

		parent := gatewayv1.ParentReference{
			Name: gatewayv1.ObjectName(spritz.Spec.Ingress.GatewayName),
		}
		if spritz.Spec.Ingress.GatewayNamespace != "" {
			parent.Namespace = gatewayNamespacePtr(spritz.Spec.Ingress.GatewayNamespace)
		}
		if spritz.Spec.Ingress.GatewaySectionName != "" {
			parent.SectionName = gatewaySectionNamePtr(spritz.Spec.Ingress.GatewaySectionName)
		}

		port := gatewayv1.PortNumber(httpServicePortNumber(spritz))
		route.Spec.ParentRefs = []gatewayv1.ParentReference{parent}
		route.Spec.Hostnames = []gatewayv1.Hostname{gatewayv1.Hostname(spritz.Spec.Ingress.Host)}
		rule := gatewayv1.HTTPRouteRule{
			Matches: []gatewayv1.HTTPRouteMatch{
				{
					Path: &gatewayv1.HTTPPathMatch{
						Type:  pathMatchTypePtr(gatewayv1.PathMatchPathPrefix),
						Value: &path,
					},
				},
			},
			BackendRefs: []gatewayv1.HTTPBackendRef{
				{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Name: gatewayv1.ObjectName(spritz.Name),
							Port: portNumberPtr(port),
						},
					},
				},
			},
		}
		if path != "/" {
			rewrite := gatewayv1.HTTPURLRewriteFilter{
				Path: &gatewayv1.HTTPPathModifier{
					Type:               gatewayv1.PrefixMatchHTTPPathModifier,
					ReplacePrefixMatch: stringPtr("/"),
				},
			}
			rule.Filters = []gatewayv1.HTTPRouteFilter{
				{
					Type:       gatewayv1.HTTPRouteFilterURLRewrite,
					URLRewrite: &rewrite,
				},
			}
		}

		route.Spec.Rules = []gatewayv1.HTTPRouteRule{rule}

		return nil
	})

	if err != nil {
		logger.Error(err, "failed to reconcile HTTPRoute", "name", spritz.Name, "namespace", spritz.Namespace)
	}
	return err
}

func (r *SpritzReconciler) reconcileStatus(ctx context.Context, spritz *spritzv1.Spritz) (*time.Duration, error) {
	logger := log.FromContext(ctx)
	now := time.Now()

	if spritz.Spec.Ingress != nil && ingressMode(spritz) == "gateway" {
		if spritz.Spec.Ingress.Host == "" {
			return nil, r.setStatus(ctx, spritz, "Error", "", buildSSHInfo(spritz), "InvalidIngress", "ingress.host is required when ingress.mode=gateway")
		}
		if spritz.Spec.Ingress.GatewayName == "" {
			return nil, r.setStatus(ctx, spritz, "Error", "", buildSSHInfo(spritz), "InvalidIngress", "ingress.gatewayName is required when ingress.mode=gateway")
		}
	}
	for _, repo := range repoEntries(spritz) {
		if err := validateRepoDir(repo.Dir); err != nil {
			return nil, r.setStatus(ctx, spritz, "Error", "", buildSSHInfo(spritz), "InvalidRepoDir", err.Error())
		}
	}

	if spritz.Spec.TTL != "" {
		ttl, err := time.ParseDuration(spritz.Spec.TTL)
		if err != nil {
			return nil, r.setStatus(ctx, spritz, "Error", "", buildSSHInfo(spritz), "InvalidTTL", "invalid ttl format")
		}
		expiry := spritz.CreationTimestamp.Add(ttl)
		expiresAt := metav1.NewTime(expiry)
		spritz.Status.ExpiresAt = &expiresAt
		grace := ttlGracePeriod()
		deleteAt := expiry.Add(grace)
		if now.After(deleteAt) {
			if err := r.setStatus(ctx, spritz, "Expired", "", buildSSHInfo(spritz), "Expired", "ttl expired"); err != nil {
				logger.Error(err, "failed to set expired status")
			}
			return nil, r.Delete(ctx, spritz)
		}
		if now.After(expiry) {
			remaining := deleteAt.Sub(now)
			if remaining < 0 {
				remaining = 0
			}
			message := fmt.Sprintf("ttl expired; deleting in %s", remaining.Round(time.Second))
			if err := r.setStatus(ctx, spritz, "Expiring", spritzURL(spritz), buildSSHInfo(spritz), "Expiring", message); err != nil {
				return nil, err
			}
			return &remaining, nil
		}
	} else {
		spritz.Status.ExpiresAt = nil
	}

	var deploy appsv1.Deployment
	if err := r.Get(ctx, client.ObjectKey{Name: spritz.Name, Namespace: spritz.Namespace}, &deploy); err != nil {
		if errors.IsNotFound(err) {
			return nil, r.setStatus(ctx, spritz, "Provisioning", "", buildSSHInfo(spritz), "Provisioning", "deployment not created yet")
		}
		return nil, err
	}

	ready := deploy.Status.AvailableReplicas > 0
	phase := "Provisioning"
	reason := "Provisioning"
	message := "waiting for deployment"
	if ready {
		phase = "Ready"
		reason = "Ready"
		message = "spritz ready"
	}

	url := spritzURL(spritz)
	return nil, r.setStatus(ctx, spritz, phase, url, buildSSHInfo(spritz), reason, message)
}

func (r *SpritzReconciler) setStatus(ctx context.Context, spritz *spritzv1.Spritz, phase, url string, sshInfo *spritzv1.SpritzSSHInfo, reason, message string) error {
	conditionStatus := metav1.ConditionFalse
	if phase == "Ready" {
		conditionStatus = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&spritz.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             conditionStatus,
		ObservedGeneration: spritz.Generation,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	})

	spritz.Status.Phase = phase
	spritz.Status.Message = message
	if url != "" {
		spritz.Status.URL = url
	}
	spritz.Status.SSH = sshInfo
	if phase == "Ready" && spritz.Status.ReadyAt == nil {
		now := metav1.Now()
		spritz.Status.ReadyAt = &now
	}

	return r.Status().Update(ctx, spritz)
}

func spritzURL(spritz *spritzv1.Spritz) string {
	if spritz.Spec.Ingress != nil && spritz.Spec.Ingress.Host != "" {
		path := spritz.Spec.Ingress.Path
		if path == "" {
			path = "/"
		}
		return fmt.Sprintf("https://%s%s", spritz.Spec.Ingress.Host, path)
	}

	if len(spritz.Spec.Ports) == 0 {
		if !isWebEnabled(spritz) {
			return ""
		}
		return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", spritz.Name, spritz.Namespace, defaultWebPort)
	}

	port := spritz.Spec.Ports[0]
	servicePort := port.ContainerPort
	if port.ServicePort != 0 {
		servicePort = port.ServicePort
	}
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", spritz.Name, spritz.Namespace, servicePort)
}

func (r *SpritzReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&spritzv1.Spritz{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&netv1.Ingress{}).
		Owns(&gatewayv1.HTTPRoute{}).
		Complete(r)
}

func baseLabels(spritz *spritzv1.Spritz) map[string]string {
	labels := map[string]string{
		"spritz.sh/name": spritz.Name,
	}
	if spritz.Spec.Owner.ID != "" {
		labels["spritz.sh/owner"] = ownerLabelValue(spritz.Spec.Owner.ID)
	}
	return labels
}

func ownerLabelValue(id string) string {
	if id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(id))
	return fmt.Sprintf("owner-%x", sum[:16])
}

func mergeMaps(base map[string]string, overlay map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

func ttlGracePeriod() time.Duration {
	value := strings.TrimSpace(os.Getenv("SPRITZ_TTL_GRACE_PERIOD"))
	if value == "" {
		return defaultTTLGrace
	}
	grace, err := time.ParseDuration(value)
	if err != nil || grace < 0 {
		return defaultTTLGrace
	}
	return grace
}

func loadHomePVCSettings() homePVCSettings {
	prefix := strings.TrimSpace(os.Getenv("SPRITZ_HOME_PVC_PREFIX"))
	if prefix == "" {
		return homePVCSettings{enabled: false, mountPaths: nil}
	}

	size := defaultHomeSizeLimit
	sizeRaw := strings.TrimSpace(os.Getenv("SPRITZ_HOME_PVC_SIZE"))
	if sizeRaw != "" {
		if parsed, err := resource.ParseQuantity(sizeRaw); err == nil {
			size = parsed
		}
	}

	accessModes := parseAccessModes(os.Getenv("SPRITZ_HOME_PVC_ACCESS_MODES"))
	if len(accessModes) == 0 {
		accessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	}

	mountPaths := parseCSV(os.Getenv("SPRITZ_HOME_MOUNT_PATHS"))
	return homePVCSettings{
		enabled:      true,
		prefix:       prefix,
		size:         size,
		accessModes:  accessModes,
		storageClass: strings.TrimSpace(os.Getenv("SPRITZ_HOME_PVC_STORAGE_CLASS")),
		mountPaths:   mountPaths,
	}
}

func loadSharedConfigPVCSettings() sharedConfigPVCSettings {
	prefix := strings.TrimSpace(os.Getenv("SPRITZ_SHARED_CONFIG_PVC_PREFIX"))
	if prefix == "" {
		return sharedConfigPVCSettings{enabled: false}
	}

	size := defaultSharedConfigSize
	sizeRaw := strings.TrimSpace(os.Getenv("SPRITZ_SHARED_CONFIG_PVC_SIZE"))
	if sizeRaw != "" {
		if parsed, err := resource.ParseQuantity(sizeRaw); err == nil {
			size = parsed
		}
	}

	accessModes := parseAccessModes(os.Getenv("SPRITZ_SHARED_CONFIG_PVC_ACCESS_MODES"))
	if len(accessModes) == 0 {
		accessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}
	}

	mountPath := strings.TrimSpace(os.Getenv("SPRITZ_SHARED_CONFIG_MOUNT_PATH"))
	if mountPath == "" {
		mountPath = defaultSharedConfigMount
	}

	return sharedConfigPVCSettings{
		enabled:      true,
		prefix:       prefix,
		size:         size,
		accessModes:  accessModes,
		storageClass: strings.TrimSpace(os.Getenv("SPRITZ_SHARED_CONFIG_PVC_STORAGE_CLASS")),
		mountPath:    mountPath,
	}
}

func loadPodNodeSelector() (map[string]string, error) {
	raw := strings.TrimSpace(os.Getenv("SPRITZ_POD_NODE_SELECTOR"))
	if raw == "" {
		return nil, nil
	}
	return parseNodeSelector(raw)
}

func parseAccessModes(raw string) []corev1.PersistentVolumeAccessMode {
	modes := []corev1.PersistentVolumeAccessMode{}
	for _, item := range parseCSV(raw) {
		switch strings.ToLower(item) {
		case "readwriteonce", "rwo":
			modes = append(modes, corev1.ReadWriteOnce)
		case "readwritemany", "rwx":
			modes = append(modes, corev1.ReadWriteMany)
		case "readonlymany", "rox":
			modes = append(modes, corev1.ReadOnlyMany)
		}
	}
	return modes
}

func parseCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := []string{}
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func parseNodeSelector(raw string) (map[string]string, error) {
	selector := map[string]string{}
	for _, part := range parseCSV(raw) {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid node selector entry: %s", part)
		}
		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])
		if key == "" || value == "" {
			return nil, fmt.Errorf("invalid node selector entry: %s", part)
		}
		selector[key] = value
	}
	if len(selector) == 0 {
		return nil, nil
	}
	return selector, nil
}

func buildPodSecurityContext(homePVCEnabled bool, sharedConfigPVCEnabled bool, sharedMountsEnabled bool, repoInitEnabled bool) *corev1.PodSecurityContext {
	if !homePVCEnabled && !sharedConfigPVCEnabled && !sharedMountsEnabled && !repoInitEnabled {
		return nil
	}
	fsGroup := repoInitGroupID
	return &corev1.PodSecurityContext{FSGroup: &fsGroup}
}

func appendUniqueMounts(mounts []corev1.VolumeMount, additions ...corev1.VolumeMount) []corev1.VolumeMount {
	seen := map[string]bool{}
	for _, mount := range mounts {
		seen[mount.MountPath] = true
	}
	for _, mount := range additions {
		if seen[mount.MountPath] {
			continue
		}
		seen[mount.MountPath] = true
		mounts = append(mounts, mount)
	}
	return mounts
}

func ensureMount(mounts []corev1.VolumeMount, mount corev1.VolumeMount) []corev1.VolumeMount {
	for _, existing := range mounts {
		if existing.MountPath == mount.MountPath {
			return mounts
		}
	}
	return append(mounts, mount)
}

func pathHasPrefix(path, prefix string) bool {
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+"/")
}

func repoDirNeedsWorkspaceMount(repoDir string, mountRoots []corev1.VolumeMount) bool {
	if pathHasPrefix(repoDir, "/workspace") {
		return false
	}
	for _, mount := range mountRoots {
		if pathHasPrefix(repoDir, mount.MountPath) {
			return false
		}
	}
	return true
}

func appendRepoDirMount(mounts []corev1.VolumeMount, repoDir string, needsMount bool) []corev1.VolumeMount {
	if !needsMount {
		return mounts
	}
	return append(mounts, corev1.VolumeMount{Name: "workspace", MountPath: repoDir})
}

func appendRepoDirMounts(mounts []corev1.VolumeMount, repoDirs []string, mountRoots []corev1.VolumeMount) []corev1.VolumeMount {
	seen := map[string]bool{}
	for _, mount := range mounts {
		seen[mount.MountPath] = true
	}
	for _, repoDir := range repoDirs {
		if repoDir == "" {
			continue
		}
		if !repoDirNeedsWorkspaceMount(repoDir, mountRoots) {
			continue
		}
		if seen[repoDir] {
			continue
		}
		seen[repoDir] = true
		mounts = append(mounts, corev1.VolumeMount{Name: "workspace", MountPath: repoDir})
	}
	return mounts
}

func buildHomeMounts(settings homePVCSettings) []corev1.VolumeMount {
	paths := settings.mountPaths
	if len(paths) == 0 {
		paths = []string{"/home/dev"}
	}
	seen := map[string]bool{}
	mounts := []corev1.VolumeMount{}
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		mounts = append(mounts, corev1.VolumeMount{Name: "home", MountPath: path})
	}
	return mounts
}

type repoAuthConfig struct {
	secretName  string
	netrcKey    string
	usernameKey string
	passwordKey string
	host        string
}

const repoInitScript = `
set -eu

mkdir -p "$SPRITZ_REPO_DIR"

if [ -n "${SPRITZ_REPO_AUTH_NETRC_PATH:-}" ] && [ -f "$SPRITZ_REPO_AUTH_NETRC_PATH" ]; then
  mkdir -p "$HOME"
  cp "$SPRITZ_REPO_AUTH_NETRC_PATH" "$HOME/.netrc"
  chmod 0600 "$HOME/.netrc"
elif [ -n "${SPRITZ_REPO_AUTH_USERNAME:-}" ] && [ -n "${SPRITZ_REPO_AUTH_PASSWORD:-}" ] && [ -n "${SPRITZ_REPO_AUTH_HOST:-}" ]; then
  mkdir -p "$HOME"
  cat > "$HOME/.netrc" <<EOF
machine ${SPRITZ_REPO_AUTH_HOST}
  login ${SPRITZ_REPO_AUTH_USERNAME}
  password ${SPRITZ_REPO_AUTH_PASSWORD}
EOF
  chmod 0600 "$HOME/.netrc"
fi

	fetch_cmd() {
  set -- git fetch --prune
  if [ -n "${SPRITZ_REPO_DEPTH:-}" ]; then
    set -- "$@" --depth "${SPRITZ_REPO_DEPTH}"
  fi
  set -- "$@" origin
  "$@"
	}

	clone_cmd() {
  set -- git clone
  if [ -n "${SPRITZ_REPO_DEPTH:-}" ]; then
    set -- "$@" --depth "${SPRITZ_REPO_DEPTH}"
  fi
  if [ -n "${SPRITZ_REPO_BRANCH:-}" ]; then
    set -- "$@" --branch "${SPRITZ_REPO_BRANCH}"
  fi
  set -- "$@" "$SPRITZ_REPO_URL" "$SPRITZ_REPO_DIR"
  "$@"
	}

if [ -d "$SPRITZ_REPO_DIR/.git" ]; then
  cd "$SPRITZ_REPO_DIR"
  git remote set-url origin "$SPRITZ_REPO_URL"
  fetch_cmd
	else
  clone_cmd
	  cd "$SPRITZ_REPO_DIR"
	fi

if [ -n "${SPRITZ_REPO_REVISION:-}" ]; then
  git checkout "$SPRITZ_REPO_REVISION" || (git fetch origin "$SPRITZ_REPO_REVISION" && git checkout "$SPRITZ_REPO_REVISION")
fi

if [ "${SPRITZ_REPO_SUBMODULES:-false}" = "true" ]; then
  git submodule update --init --recursive
fi

	if [ -n "${SPRITZ_REPO_GID:-}" ]; then
  chgrp -R "${SPRITZ_REPO_GID}" "$SPRITZ_REPO_DIR"
  chmod -R g+rwX "$SPRITZ_REPO_DIR"
fi
`

func buildRepoInitContainers(
	spritz *spritzv1.Spritz,
	repos []spritzv1.SpritzRepo,
	mountRoots []corev1.VolumeMount,
) ([]corev1.Container, []corev1.Volume, error) {
	if len(repos) == 0 {
		return nil, nil, nil
	}

	var containers []corev1.Container
	var volumes []corev1.Volume
	for i, repo := range repos {
		if strings.TrimSpace(repo.URL) == "" {
			continue
		}
		repoDir := repoDirFor(repo, i, len(repos))
		needsRepoDirMount := repoDirNeedsWorkspaceMount(repoDir, mountRoots)
		container, authVolume, err := buildRepoInitContainerForRepo(spritz, &repo, repoDir, needsRepoDirMount, mountRoots, i)
		if err != nil {
			return nil, nil, err
		}
		if container != nil {
			containers = append(containers, *container)
		}
		if authVolume != nil {
			volumes = append(volumes, *authVolume)
		}
	}

	if len(containers) == 0 {
		return nil, nil, nil
	}
	return containers, volumes, nil
}

func buildRepoInitContainerForRepo(
	spritz *spritzv1.Spritz,
	repo *spritzv1.SpritzRepo,
	repoDir string,
	needsRepoDirMount bool,
	mountRoots []corev1.VolumeMount,
	index int,
) (*corev1.Container, *corev1.Volume, error) {
	if repo == nil || strings.TrimSpace(repo.URL) == "" {
		return nil, nil, nil
	}

	authConfig, err := repoAuthConfigFromSpec(repo)
	if err != nil {
		return nil, nil, err
	}

	env := []corev1.EnvVar{
		{Name: "SPRITZ_REPO_URL", Value: repo.URL},
		{Name: "SPRITZ_REPO_DIR", Value: repoDir},
		{Name: "HOME", Value: repoInitHomeDir},
		{Name: "GIT_TERMINAL_PROMPT", Value: "0"},
		{Name: "SPRITZ_REPO_GID", Value: fmt.Sprintf("%d", repoInitGroupID)},
	}
	if repo.Branch != "" {
		env = append(env, corev1.EnvVar{Name: "SPRITZ_REPO_BRANCH", Value: repo.Branch})
	}
	if repo.Revision != "" {
		env = append(env, corev1.EnvVar{Name: "SPRITZ_REPO_REVISION", Value: repo.Revision})
	}
	if repo.Depth > 0 {
		env = append(env, corev1.EnvVar{Name: "SPRITZ_REPO_DEPTH", Value: fmt.Sprintf("%d", repo.Depth)})
	}
	if repo.Submodules {
		env = append(env, corev1.EnvVar{Name: "SPRITZ_REPO_SUBMODULES", Value: "true"})
	}

	var authVolume *corev1.Volume
	volumeMounts := []corev1.VolumeMount{
		{Name: "workspace", MountPath: "/workspace"},
	}
	volumeMounts = appendUniqueMounts(volumeMounts, mountRoots...)
	volumeMounts = ensureMount(volumeMounts, corev1.VolumeMount{Name: "home", MountPath: repoInitHomeDir})
	volumeMounts = appendRepoDirMount(volumeMounts, repoDir, needsRepoDirMount)
	if authConfig != nil {
		authVolumeName := fmt.Sprintf("repo-auth-%d", index)
		authVolume = &corev1.Volume{
			Name: authVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: authConfig.secretName},
			},
		}
		volumeMounts = append(volumeMounts, corev1.VolumeMount{Name: authVolumeName, MountPath: repoAuthMountPath, ReadOnly: true})
		if authConfig.netrcKey != "" {
			env = append(env, corev1.EnvVar{
				Name:  "SPRITZ_REPO_AUTH_NETRC_PATH",
				Value: fmt.Sprintf("%s/%s", repoAuthMountPath, authConfig.netrcKey),
			})
		}
		if authConfig.usernameKey != "" && authConfig.passwordKey != "" {
			env = append(env,
				corev1.EnvVar{
					Name: "SPRITZ_REPO_AUTH_USERNAME",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: authConfig.secretName},
							Key:                  authConfig.usernameKey,
						},
					},
				},
				corev1.EnvVar{
					Name: "SPRITZ_REPO_AUTH_PASSWORD",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: authConfig.secretName},
							Key:                  authConfig.passwordKey,
						},
					},
				},
			)
			if authConfig.host != "" {
				env = append(env, corev1.EnvVar{Name: "SPRITZ_REPO_AUTH_HOST", Value: authConfig.host})
			}
		}
	}

	container := corev1.Container{
		Name:         fmt.Sprintf("repo-init-%d", index),
		Image:        repoInitImage(),
		Command:      []string{"/bin/sh", "-ec", repoInitScript},
		Env:          env,
		VolumeMounts: volumeMounts,
	}

	return &container, authVolume, nil
}

func repoAuthConfigFromSpec(repo *spritzv1.SpritzRepo) (*repoAuthConfig, error) {
	if repo == nil || repo.Auth == nil {
		return nil, nil
	}

	if repo.Auth.SecretName == "" {
		return nil, fmt.Errorf("repo.auth.secretName is required when repo.auth is set")
	}

	cfg := &repoAuthConfig{
		secretName:  repo.Auth.SecretName,
		netrcKey:    repo.Auth.NetrcKey,
		usernameKey: repo.Auth.UsernameKey,
		passwordKey: repo.Auth.PasswordKey,
	}

	if cfg.netrcKey == "" && cfg.usernameKey == "" && cfg.passwordKey == "" {
		cfg.netrcKey = "netrc"
	}

	if (cfg.usernameKey != "" || cfg.passwordKey != "") && (cfg.usernameKey == "" || cfg.passwordKey == "") {
		return nil, fmt.Errorf("repo.auth.usernameKey and repo.auth.passwordKey must both be set when using basic auth")
	}

	if cfg.usernameKey != "" && cfg.passwordKey != "" {
		cfg.host = repoAuthHost(repo.URL)
		if cfg.host == "" {
			return nil, fmt.Errorf("repo.auth requires a host in repo.url when using basic auth")
		}
	}

	return cfg, nil
}

func repoAuthHost(repoURL string) string {
	repoURL = strings.TrimSpace(repoURL)
	if repoURL == "" {
		return ""
	}
	if !strings.Contains(repoURL, "://") {
		repoURL = "https://" + repoURL
	}
	parsed, err := url.Parse(repoURL)
	if err == nil && parsed.Host != "" {
		return parsed.Hostname()
	}

	if at := strings.Index(repoURL, "@"); at != -1 {
		rest := repoURL[at+1:]
		if colon := strings.Index(rest, ":"); colon != -1 {
			return rest[:colon]
		}
		if slash := strings.Index(rest, "/"); slash != -1 {
			return rest[:slash]
		}
	}
	return ""
}

func repoInitImage() string {
	if value := strings.TrimSpace(os.Getenv("SPRITZ_GIT_INIT_IMAGE")); value != "" {
		return value
	}
	return defaultRepoInitImage
}

func (r *SpritzReconciler) ensureHomePVC(
	ctx context.Context,
	spritz *spritzv1.Spritz,
	name string,
	settings homePVCSettings,
) error {
	labels := map[string]string{
		"spritz.sh/purpose": "home",
	}
	if spritz.Spec.Owner.ID != "" {
		labels["spritz.sh/owner"] = ownerLabelValue(spritz.Spec.Owner.ID)
	}

	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: spritz.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, pvc, func() error {
		// Intentionally no owner reference: per-owner PVCs should outlive individual Spritz instances.
		pvc.Labels = mergeMaps(pvc.Labels, labels)
		if pvc.CreationTimestamp.IsZero() {
			pvc.Spec = corev1.PersistentVolumeClaimSpec{
				AccessModes: settings.accessModes,
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: settings.size,
					},
				},
			}
			if settings.storageClass != "" {
				pvc.Spec.StorageClassName = &settings.storageClass
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	switch pvc.Status.Phase {
	case "", corev1.ClaimBound, corev1.ClaimPending:
		return nil
	case corev1.ClaimLost:
		return fmt.Errorf("home PVC %s lost", name)
	default:
		return fmt.Errorf("home PVC %s not bound (status=%s)", name, pvc.Status.Phase)
	}
	return nil
}

func (r *SpritzReconciler) ensureSharedConfigPVC(
	ctx context.Context,
	spritz *spritzv1.Spritz,
	name string,
	settings sharedConfigPVCSettings,
) error {
	labels := map[string]string{
		"spritz.sh/purpose": "shared-config",
	}
	if spritz.Spec.Owner.ID != "" {
		labels["spritz.sh/owner"] = ownerLabelValue(spritz.Spec.Owner.ID)
	}

	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: spritz.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, pvc, func() error {
		// Intentionally no owner reference: shared config PVCs should outlive individual Spritz instances.
		pvc.Labels = mergeMaps(pvc.Labels, labels)
		if pvc.CreationTimestamp.IsZero() {
			pvc.Spec = corev1.PersistentVolumeClaimSpec{
				AccessModes: settings.accessModes,
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: settings.size,
					},
				},
			}
			if settings.storageClass != "" {
				pvc.Spec.StorageClassName = &settings.storageClass
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	switch pvc.Status.Phase {
	case "", corev1.ClaimBound, corev1.ClaimPending:
		return nil
	case corev1.ClaimLost:
		return fmt.Errorf("shared config PVC %s lost", name)
	default:
		return fmt.Errorf("shared config PVC %s not bound (status=%s)", name, pvc.Status.Phase)
	}
}

func validateMountPaths(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	cleaned := []string{}
	seen := map[string]bool{}
	for _, raw := range paths {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "/") {
			return fmt.Errorf("home mount path must be absolute: %s", trimmed)
		}
		path := strings.TrimRight(trimmed, "/")
		if path == "" {
			return fmt.Errorf("home mount path must not be root: %s", trimmed)
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		cleaned = append(cleaned, path)
	}
	for i, base := range cleaned {
		for j, other := range cleaned {
			if i == j {
				continue
			}
			if strings.HasPrefix(other, base+"/") {
				return fmt.Errorf("home mount paths overlap: %s and %s", base, other)
			}
		}
	}
	return nil
}

func validateSharedConfigMountPath(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("shared config mount path must be set")
	}
	if !strings.HasPrefix(trimmed, "/") {
		return fmt.Errorf("shared config mount path must be absolute: %s", trimmed)
	}
	path := strings.TrimRight(trimmed, "/")
	if path == "" {
		return fmt.Errorf("shared config mount path must not be root: %s", trimmed)
	}
	return nil
}

func ownerPVCName(prefix, id string) string {
	if id == "" {
		return prefix
	}
	sum := sha256.Sum256([]byte(id))
	return fmt.Sprintf("%s-owner-%x", prefix, sum[:])
}

func emptyDirSizeLimit(key string, fallback resource.Quantity) *resource.Quantity {
	value := strings.TrimSpace(os.Getenv(key))
	if value != "" {
		if qty, err := resource.ParseQuantity(value); err == nil {
			q := qty
			return &q
		}
	}
	q := fallback
	return &q
}

func isWebEnabled(spritz *spritzv1.Spritz) bool {
	if spritz.Spec.Features == nil {
		return true
	}
	if spritz.Spec.Features.Web == nil {
		return true
	}
	return *spritz.Spec.Features.Web
}

func isSSHEnabled(spritz *spritzv1.Spritz) bool {
	if spritz.Spec.SSH != nil && spritz.Spec.SSH.Enabled {
		return true
	}
	if spritz.Spec.Features == nil {
		return false
	}
	if spritz.Spec.Features.SSH == nil {
		return false
	}
	return *spritz.Spec.Features.SSH
}

func sshMode(spritz *spritzv1.Spritz) string {
	if spritz.Spec.SSH != nil && spritz.Spec.SSH.Mode != "" {
		return spritz.Spec.SSH.Mode
	}
	return defaultSSHMode
}

func sshConfig(spritz *spritzv1.Spritz) spritzv1.SpritzSSH {
	cfg := spritzv1.SpritzSSH{}
	if spritz.Spec.SSH != nil {
		cfg = *spritz.Spec.SSH
	}
	if cfg.ContainerPort == 0 {
		cfg.ContainerPort = defaultSSHPort
	}
	if cfg.ServicePort == 0 {
		cfg.ServicePort = cfg.ContainerPort
	}
	if cfg.GatewayPort == 0 {
		cfg.GatewayPort = defaultSSHPort
	}
	if cfg.User == "" {
		cfg.User = defaultSSHUser
	}
	if cfg.Mode == "" {
		cfg.Mode = defaultSSHMode
	}
	return cfg
}

func shouldExposeSSHService(spritz *spritzv1.Spritz) bool {
	if !isSSHEnabled(spritz) {
		return false
	}
	return sshMode(spritz) != "gateway"
}

func containerPorts(spritz *spritzv1.Spritz) []corev1.ContainerPort {
	if len(spritz.Spec.Ports) == 0 && !isWebEnabled(spritz) {
		if !isSSHEnabled(spritz) {
			return nil
		}
	}

	if len(spritz.Spec.Ports) == 0 {
		ports := []corev1.ContainerPort{}
		if isWebEnabled(spritz) {
			ports = append(ports, corev1.ContainerPort{Name: "http", ContainerPort: defaultWebPort, Protocol: corev1.ProtocolTCP})
		}
		if isSSHEnabled(spritz) {
			cfg := sshConfig(spritz)
			ports = append(ports, corev1.ContainerPort{Name: "ssh", ContainerPort: cfg.ContainerPort, Protocol: corev1.ProtocolTCP})
		}
		return ports
	}

	ports := make([]corev1.ContainerPort, 0, len(spritz.Spec.Ports))
	for _, port := range spritz.Spec.Ports {
		protocol := port.Protocol
		if protocol == "" {
			protocol = corev1.ProtocolTCP
		}
		ports = append(ports, corev1.ContainerPort{
			Name:          port.Name,
			ContainerPort: port.ContainerPort,
			Protocol:      protocol,
		})
	}
	if isSSHEnabled(spritz) {
		cfg := sshConfig(spritz)
		if !hasSpecPort(spritz, cfg.ContainerPort, "ssh") {
			ports = append(ports, corev1.ContainerPort{Name: "ssh", ContainerPort: cfg.ContainerPort, Protocol: corev1.ProtocolTCP})
		}
	}
	return ports
}

func servicePorts(spritz *spritzv1.Spritz) []corev1.ServicePort {
	if len(spritz.Spec.Ports) == 0 {
		ports := []corev1.ServicePort{}
		if isWebEnabled(spritz) {
			ports = append(ports, corev1.ServicePort{
				Name:       "http",
				Port:       defaultWebPort,
				TargetPort: intstrFromInt(defaultWebPort),
				Protocol:   corev1.ProtocolTCP,
			})
		}
		if shouldExposeSSHService(spritz) {
			cfg := sshConfig(spritz)
			ports = append(ports, corev1.ServicePort{
				Name:       "ssh",
				Port:       cfg.ServicePort,
				TargetPort: intstrFromInt(cfg.ContainerPort),
				Protocol:   corev1.ProtocolTCP,
			})
		}
		return ports
	}

	ports := make([]corev1.ServicePort, 0, len(spritz.Spec.Ports))
	for _, port := range spritz.Spec.Ports {
		servicePort := port.ContainerPort
		if port.ServicePort != 0 {
			servicePort = port.ServicePort
		}
		protocol := port.Protocol
		if protocol == "" {
			protocol = corev1.ProtocolTCP
		}
		ports = append(ports, corev1.ServicePort{
			Name:       port.Name,
			Port:       servicePort,
			TargetPort: intstrFromInt(port.ContainerPort),
			Protocol:   protocol,
		})
	}
	if shouldExposeSSHService(spritz) {
		cfg := sshConfig(spritz)
		if !hasSpecPort(spritz, cfg.ContainerPort, "ssh") {
			ports = append(ports, corev1.ServicePort{
				Name:       "ssh",
				Port:       cfg.ServicePort,
				TargetPort: intstrFromInt(cfg.ContainerPort),
				Protocol:   corev1.ProtocolTCP,
			})
		}
	}
	return ports
}

func intstrFromInt(value int32) intstr.IntOrString {
	return intstr.FromInt(int(value))
}

func httpPortName(spritz *spritzv1.Spritz) string {
	if len(spritz.Spec.Ports) == 0 {
		return "http"
	}
	for _, port := range spritz.Spec.Ports {
		if port.Name == "http" {
			return "http"
		}
	}
	if spritz.Spec.Ports[0].Name != "" {
		return spritz.Spec.Ports[0].Name
	}
	return "http"
}

func httpServicePortNumber(spritz *spritzv1.Spritz) int32 {
	if len(spritz.Spec.Ports) == 0 {
		return defaultWebPort
	}
	for _, port := range spritz.Spec.Ports {
		if port.Name == "http" {
			if port.ServicePort != 0 {
				return port.ServicePort
			}
			return port.ContainerPort
		}
	}
	port := spritz.Spec.Ports[0]
	if port.ServicePort != 0 {
		return port.ServicePort
	}
	return port.ContainerPort
}

func ingressMode(spritz *spritzv1.Spritz) string {
	if spritz.Spec.Ingress == nil || spritz.Spec.Ingress.Mode == "" {
		return "ingress"
	}
	return strings.ToLower(spritz.Spec.Ingress.Mode)
}

func shouldUseIngress(spritz *spritzv1.Spritz) bool {
	if spritz.Spec.Ingress == nil || spritz.Spec.Ingress.Host == "" {
		return false
	}
	return ingressMode(spritz) != "gateway"
}

func shouldUseGatewayRoute(spritz *spritzv1.Spritz) bool {
	if spritz.Spec.Ingress == nil || spritz.Spec.Ingress.Host == "" {
		return false
	}
	return ingressMode(spritz) == "gateway"
}

func pathMatchTypePtr(value gatewayv1.PathMatchType) *gatewayv1.PathMatchType {
	return &value
}

func portNumberPtr(value gatewayv1.PortNumber) *gatewayv1.PortNumber {
	return &value
}

func gatewayNamespacePtr(value string) *gatewayv1.Namespace {
	namespace := gatewayv1.Namespace(value)
	return &namespace
}

func gatewaySectionNamePtr(value string) *gatewayv1.SectionName {
	name := gatewayv1.SectionName(value)
	return &name
}

func stringPtr(value string) *string {
	return &value
}

func pathTypePtr(pathType netv1.PathType) *netv1.PathType {
	return &pathType
}

func hasSpecPort(spritz *spritzv1.Spritz, port int32, name string) bool {
	for _, specPort := range spritz.Spec.Ports {
		if specPort.ContainerPort == port || specPort.Name == name {
			return true
		}
	}
	return false
}

func buildSSHInfo(spritz *spritzv1.Spritz) *spritzv1.SpritzSSHInfo {
	if !isSSHEnabled(spritz) {
		return nil
	}

	cfg := sshConfig(spritz)
	info := &spritzv1.SpritzSSHInfo{
		User: cfg.User,
	}

	if cfg.Mode == "gateway" {
		if cfg.GatewayService == "" {
			return info
		}
		namespace := cfg.GatewayNamespace
		if namespace == "" {
			namespace = spritz.Namespace
		}
		info.Host = fmt.Sprintf("%s.%s.svc.cluster.local", cfg.GatewayService, namespace)
		info.Port = cfg.GatewayPort
		return info
	}

	info.Host = fmt.Sprintf("%s.%s.svc.cluster.local", spritz.Name, spritz.Namespace)
	info.Port = cfg.ServicePort
	return info
}
