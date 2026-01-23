package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type spritzReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Config     config
	HTTPClient httpClient
}

type httpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

var spritzGVK = schema.GroupVersionKind{Group: "spritz.sh", Version: "v1", Kind: "Spritz"}

const (
	tokenExpiryAnnotation = "spritz.sh/github-app-token-expires-at"
	tokenRepoAnnotation   = "spritz.sh/github-app-repo"
	tokenRefreshLead      = 15 * time.Minute
	labelManagedBy        = "spritz.sh/managedBy"
	labelPurpose          = "spritz.sh/purpose"
	integrationName       = "github-app-integration"
	purposeRepoAuth       = "repo-auth"
	netrcKey              = "netrc"
	netrcLoginToken       = "x-access-token"
)

func (r *spritzReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := ctrl.LoggerFrom(ctx).WithValues("spritz", req.NamespacedName)

	var spritz unstructured.Unstructured
	spritz.SetGroupVersionKind(spritzGVK)
	if err := r.Get(ctx, req.NamespacedName, &spritz); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	spritz.SetGroupVersionKind(spritzGVK)
	if spritz.GetDeletionTimestamp() != nil {
		return ctrl.Result{}, nil
	}

	if !hasIntegrationAnnotation(&spritz, r.Config.AnnotationKey, r.Config.AnnotationValue) {
		return ctrl.Result{}, nil
	}
	ownerID, _, _ := unstructured.NestedString(spritz.Object, "spec", "owner", "id")
	if ownerID == "" {
		logger.Info("owner id missing; skipping")
		return ctrl.Result{}, nil
	}

	repoSlice, hasRepos, _ := unstructured.NestedSlice(spritz.Object, "spec", "repos")
	usingRepos := hasRepos && len(repoSlice) > 0

	original := spritz.DeepCopy()
	shouldPatch := false
	var updatedRepos []interface{}
	var minRequeue *time.Duration

	updateMinRequeue := func(value time.Duration) {
		if value <= 0 {
			return
		}
		if minRequeue == nil || value < *minRequeue {
			minRequeue = &value
		}
	}

	processRepo := func(repo map[string]interface{}, index int) error {
		repoURL, authSecretName := readRepoSpec(repo)
		if repoURL == "" {
			return nil
		}

		repoHost, repoPath, err := parseRepoURL(repoURL)
		if err != nil {
			return r.recordError(logger, "invalid repo url", err)
		}
		if err := validateRepoPath(repoPath); err != nil {
			return r.recordError(logger, "invalid repo path", err)
		}
		if !r.allowedHost(repoHost) {
			logger.Info("repo host not allowed", "host", repoHost)
			return nil
		}

		secretName := repoAuthSecretName(spritz.GetName(), repoPath)
		if authSecretName != "" && authSecretName != secretName {
			return nil
		}

		secret := &corev1.Secret{}
		secretKey := client.ObjectKey{Name: secretName, Namespace: spritz.GetNamespace()}
		secretExists := true
		if err := r.Get(ctx, secretKey, secret); err != nil {
			if !errors.IsNotFound(err) {
				return err
			}
			secretExists = false
		}

		managedSecret := secretExists && isManagedByIntegration(secret)
		if secretExists && !managedSecret {
			return nil
		}

		shouldPatchAuth := shouldPatchRepoAuth(authSecretName, secretExists, managedSecret)
		if secretExists {
			shouldRefresh, requeueAfter := tokenNeedsRefresh(secret, time.Now(), repoPath)
			updateMinRequeue(requeueAfter)
			if !shouldRefresh && !shouldPatchAuth {
				return nil
			}
			if !shouldRefresh && shouldPatchAuth {
				setRepoAuth(repo, secretName)
				shouldPatch = true
				return nil
			}
		}

		token, expiry, err := r.githubAppInstallationToken(ctx, repoPath)
		if err != nil {
			return r.recordError(logger, "token mint failed", err)
		}
		netrc := buildNetrc(repoHost, token)

		secret = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: spritz.GetNamespace(),
		}}
		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
			if err := controllerutil.SetOwnerReference(&spritz, secret, r.Scheme); err != nil {
				return err
			}
			if secret.Labels == nil {
				secret.Labels = map[string]string{}
			}
			secret.Labels[labelManagedBy] = integrationName
			secret.Labels[labelPurpose] = purposeRepoAuth
			if secret.Annotations == nil {
				secret.Annotations = map[string]string{}
			}
			if expiry != nil {
				secret.Annotations[tokenExpiryAnnotation] = expiry.Format(time.RFC3339)
			} else {
				delete(secret.Annotations, tokenExpiryAnnotation)
			}
			secret.Annotations[tokenRepoAnnotation] = repoPath
			secret.Type = corev1.SecretTypeOpaque
			secret.Data = map[string][]byte{
				netrcKey: []byte(netrc),
			}
			return nil
		})
		if err != nil {
			return err
		}

		if shouldPatchAuth || authSecretName == "" {
			setRepoAuth(repo, secretName)
			shouldPatch = true
		}

		logger.Info("repo auth injected", "secret", secretName, "index", index)
		if expiry != nil {
			updateMinRequeue(time.Until(expiry.Add(-tokenRefreshLead)))
		} else {
			updateMinRequeue(tokenRefreshLead)
		}
		return nil
	}

	if usingRepos {
		for i, item := range repoSlice {
			repoMap, ok := item.(map[string]interface{})
			if !ok {
				updatedRepos = append(updatedRepos, item)
				continue
			}
			if err := processRepo(repoMap, i); err != nil {
				return ctrl.Result{}, err
			}
			updatedRepos = append(updatedRepos, repoMap)
		}
		if shouldPatch {
			if err := unstructured.SetNestedSlice(spritz.Object, updatedRepos, "spec", "repos"); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		repoMap, found, _ := unstructured.NestedMap(spritz.Object, "spec", "repo")
		if !found || len(repoMap) == 0 {
			logger.V(1).Info("repo url missing; skipping integration")
			return ctrl.Result{}, nil
		}
		if err := processRepo(repoMap, 0); err != nil {
			return ctrl.Result{}, err
		}
		if shouldPatch {
			if err := unstructured.SetNestedField(spritz.Object, repoMap, "spec", "repo"); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	if shouldPatch {
		if err := r.Patch(ctx, &spritz, client.MergeFrom(original)); err != nil {
			if errors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, err
		}
	}

	if minRequeue != nil {
		return ctrl.Result{RequeueAfter: *minRequeue}, nil
	}
	return ctrl.Result{RequeueAfter: tokenRefreshLead}, nil
}

func shouldPatchRepoAuth(authSecretName string, secretExists bool, managedSecret bool) bool {
	if authSecretName != "" {
		return false
	}
	return secretExists && managedSecret
}

func (r *spritzReconciler) patchRepoAuth(ctx context.Context, spritz *unstructured.Unstructured, secretName string) error {
	original := spritz.DeepCopy()
	auth := map[string]interface{}{
		"secretName": secretName,
		"netrcKey":   netrcKey,
	}
	if err := unstructured.SetNestedField(spritz.Object, auth, "spec", "repo", "auth"); err != nil {
		return err
	}
	return r.Patch(ctx, spritz, client.MergeFrom(original))
}

func (r *spritzReconciler) SetupWithManager(mgr ctrl.Manager) error {
	pred := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return hasIntegrationAnnotation(obj, r.Config.AnnotationKey, r.Config.AnnotationValue)
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "spritz.sh/v1",
			"kind":       "Spritz",
		}}).
		WithEventFilter(pred).
		Owns(&corev1.Secret{}).
		Complete(r)
}

func (r *spritzReconciler) recordError(logger logr.Logger, msg string, err error) error {
	logger.Error(err, msg)
	return err
}

func hasIntegrationAnnotation(obj client.Object, key, value string) bool {
	if obj == nil {
		return false
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false
	}
	if value == "" {
		_, ok := annotations[key]
		return ok
	}
	return annotations[key] == value
}

func isManagedByIntegration(secret *corev1.Secret) bool {
	if secret == nil {
		return false
	}
	return secret.Labels[labelManagedBy] == integrationName
}

func tokenNeedsRefresh(secret *corev1.Secret, now time.Time, repoPath string) (bool, time.Duration) {
	if secret == nil {
		return true, 0
	}
	if secret.Annotations != nil {
		currentRepo := secret.Annotations[tokenRepoAnnotation]
		if currentRepo != "" && repoPath != "" && currentRepo != repoPath {
			return true, 0
		}
	}
	expiryRaw := ""
	if secret.Annotations != nil {
		expiryRaw = secret.Annotations[tokenExpiryAnnotation]
	}
	if expiryRaw == "" {
		return true, 0
	}
	expiry, err := time.Parse(time.RFC3339, expiryRaw)
	if err != nil {
		return true, 0
	}
	refreshAt := expiry.Add(-tokenRefreshLead)
	if now.After(refreshAt) {
		return true, 0
	}
	return false, time.Until(refreshAt)
}

func (r *spritzReconciler) allowedHost(host string) bool {
	if host == "" {
		return false
	}
	for _, allowed := range r.Config.AllowedHosts {
		if strings.EqualFold(strings.TrimSpace(allowed), host) {
			return true
		}
	}
	return false
}

func repoAuthSecretName(name, repoPath string) string {
	base := fmt.Sprintf("%s:%s", name, repoPath)
	sum := sha256.Sum256([]byte(base))
	return fmt.Sprintf("spritz-repo-auth-%x", sum[:16])
}

func readRepoSpec(repo map[string]interface{}) (string, string) {
	if repo == nil {
		return "", ""
	}
	repoURL, _ := repo["url"].(string)
	authSecretName := ""
	if authRaw, ok := repo["auth"].(map[string]interface{}); ok {
		if secret, ok := authRaw["secretName"].(string); ok {
			authSecretName = secret
		}
	}
	return strings.TrimSpace(repoURL), strings.TrimSpace(authSecretName)
}

func setRepoAuth(repo map[string]interface{}, secretName string) {
	if repo == nil {
		return
	}
	repo["auth"] = map[string]interface{}{
		"secretName": secretName,
		"netrcKey":   netrcKey,
	}
}

func parseRepoURL(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("repo url is empty")
	}
	if strings.HasPrefix(raw, "git@") {
		return "", "", fmt.Errorf("ssh repo urls are not supported; use https")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", "", err
	}
	if parsed.Scheme == "ssh" {
		return "", "", fmt.Errorf("ssh repo urls are not supported; use https")
	}
	path := strings.TrimPrefix(parsed.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	if parsed.Hostname() == "" || path == "" {
		return "", "", fmt.Errorf("invalid repo url")
	}
	return parsed.Hostname(), path, nil
}

func validateRepoPath(repo string) error {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return fmt.Errorf("repo path must be owner/repo")
	}
	if strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return fmt.Errorf("repo path must include owner and repo")
	}
	return nil
}

func buildNetrc(host, token string) string {
	return fmt.Sprintf("machine %s\n  login %s\n  password %s\n", host, netrcLoginToken, token)
}
