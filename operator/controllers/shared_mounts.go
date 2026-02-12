package controllers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"

	corev1 "k8s.io/api/core/v1"

	spritzv1 "spritz.sh/operator/api/v1"
	"spritz.sh/operator/sharedmounts"
)

type sharedMountsSettings struct {
	enabled               bool
	mounts                []sharedmounts.MountSpec
	apiURL                string
	tokenSecretName       string
	tokenSecretKey        string
	syncerImage           string
	syncerImagePullPolicy corev1.PullPolicy
}

type sharedMountRuntime struct {
	volumes          []corev1.Volume
	volumeMounts     []corev1.VolumeMount
	initContainer    *corev1.Container
	sidecarContainer *corev1.Container
	env              []corev1.EnvVar
}

func loadSharedMountsSettings() (sharedMountsSettings, error) {
	rawMounts := strings.TrimSpace(os.Getenv("SPRITZ_SHARED_MOUNTS"))
	mounts, err := sharedmounts.ParseMountsJSON(rawMounts)
	if err != nil {
		return sharedMountsSettings{}, err
	}
	if err := validateSharedMountSpecs(mounts); err != nil {
		return sharedMountsSettings{}, err
	}

	apiURL := strings.TrimSpace(os.Getenv("SPRITZ_SHARED_MOUNTS_API_URL"))
	tokenSecretName := strings.TrimSpace(os.Getenv("SPRITZ_SHARED_MOUNTS_TOKEN_SECRET_NAME"))
	tokenSecretKey := strings.TrimSpace(os.Getenv("SPRITZ_SHARED_MOUNTS_TOKEN_SECRET_KEY"))
	if tokenSecretKey == "" {
		tokenSecretKey = "token"
	}
	syncerImage := strings.TrimSpace(os.Getenv("SPRITZ_SHARED_MOUNTS_SYNCER_IMAGE"))
	enabled := apiURL != "" || tokenSecretName != "" || syncerImage != "" || len(mounts) > 0
	if !enabled {
		return sharedMountsSettings{enabled: false, mounts: mounts}, nil
	}
	if apiURL == "" {
		return sharedMountsSettings{}, fmt.Errorf("SPRITZ_SHARED_MOUNTS_API_URL is required when shared mounts are enabled")
	}
	if tokenSecretName == "" {
		return sharedMountsSettings{}, fmt.Errorf("SPRITZ_SHARED_MOUNTS_TOKEN_SECRET_NAME is required when shared mounts are enabled")
	}
	if syncerImage == "" {
		return sharedMountsSettings{}, fmt.Errorf("SPRITZ_SHARED_MOUNTS_SYNCER_IMAGE is required when shared mounts are enabled")
	}
	pullPolicy := corev1.PullIfNotPresent
	if rawPolicy := strings.TrimSpace(os.Getenv("SPRITZ_SHARED_MOUNTS_SYNCER_IMAGE_PULL_POLICY")); rawPolicy != "" {
		pullPolicy = corev1.PullPolicy(rawPolicy)
	}

	return sharedMountsSettings{
		enabled:               true,
		mounts:                mounts,
		apiURL:                apiURL,
		tokenSecretName:       tokenSecretName,
		tokenSecretKey:        tokenSecretKey,
		syncerImage:           syncerImage,
		syncerImagePullPolicy: pullPolicy,
	}, nil
}

func validateSharedMountSpecs(mounts []sharedmounts.MountSpec) error {
	if err := sharedmounts.ValidateMounts(mounts); err != nil {
		return err
	}
	for _, mount := range mounts {
		if mount.Scope != sharedmounts.ScopeOwner {
			return fmt.Errorf("unsupported shared mount scope: %s", mount.Scope)
		}
	}
	return nil
}

func buildSharedMountRuntime(spritz *spritzv1.Spritz, settings sharedMountsSettings) (sharedMountRuntime, error) {
	runtimeMounts := resolveSharedMounts(spritz.Spec.SharedMounts, settings.mounts)
	if len(runtimeMounts) == 0 {
		return sharedMountRuntime{}, nil
	}
	if !settings.enabled {
		return sharedMountRuntime{}, fmt.Errorf("shared mounts requested but operator is not configured")
	}
	if spritz.Spec.Owner.ID == "" {
		return sharedMountRuntime{}, fmt.Errorf("shared mounts require spec.owner.id")
	}
	if err := validateSharedMountSpecs(runtimeMounts); err != nil {
		return sharedMountRuntime{}, err
	}

	volumes := []corev1.Volume{}
	mounts := []corev1.VolumeMount{}
	env := []corev1.EnvVar{}

	for _, mount := range runtimeMounts {
		volumeName := sharedMountVolumeName(mount.Name)
		volumes = append(volumes, corev1.Volume{
			Name: volumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
		readOnly := mount.Mode == sharedmounts.ModeReadOnly
		mounts = append(mounts, corev1.VolumeMount{
			Name:      volumeName,
			MountPath: mount.MountPath,
			ReadOnly:  readOnly,
		})
		env = append(env, corev1.EnvVar{
			Name:  sharedMountEnvKey(mount.Name),
			Value: path.Join(mount.MountPath, "live"),
		})
	}

	syncerEnv := []corev1.EnvVar{
		{Name: "SPRITZ_SHARED_MOUNTS", Value: mustJSON(runtimeMounts)},
		{Name: "SPRITZ_SHARED_MOUNTS_API_URL", Value: settings.apiURL},
		{
			Name: "SPRITZ_SHARED_MOUNTS_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: settings.tokenSecretName},
					Key:                  settings.tokenSecretKey,
				},
			},
		},
		{Name: "SPRITZ_OWNER_ID", Value: spritz.Spec.Owner.ID},
	}

	syncerResources := defaultSharedMountSyncerResources()

	initContainer := corev1.Container{
		Name:            "shared-mounts-init",
		Image:           settings.syncerImage,
		ImagePullPolicy: settings.syncerImagePullPolicy,
		Command:         []string{"/usr/local/bin/spritz-shared-syncer"},
		Args:            []string{"--mode=init"},
		Env:             syncerEnv,
		Resources:       syncerResources,
		VolumeMounts:    sharedMountVolumeMounts(runtimeMounts),
	}
	sidecarContainer := corev1.Container{
		Name:            "shared-mounts-syncer",
		Image:           settings.syncerImage,
		ImagePullPolicy: settings.syncerImagePullPolicy,
		Command:         []string{"/usr/local/bin/spritz-shared-syncer"},
		Args:            []string{"--mode=sidecar"},
		Env:             syncerEnv,
		Resources:       syncerResources,
		VolumeMounts:    sharedMountVolumeMounts(runtimeMounts),
	}

	return sharedMountRuntime{
		volumes:          volumes,
		volumeMounts:     mounts,
		initContainer:    &initContainer,
		sidecarContainer: &sidecarContainer,
		env:              env,
	}, nil
}

func sharedMountVolumeMounts(mounts []sharedmounts.MountSpec) []corev1.VolumeMount {
	volumeMounts := []corev1.VolumeMount{}
	for _, mount := range mounts {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      sharedMountVolumeName(mount.Name),
			MountPath: mount.MountPath,
		})
	}
	return volumeMounts
}

func resolveSharedMounts(specMounts, defaultMounts []sharedmounts.MountSpec) []sharedmounts.MountSpec {
	if len(specMounts) > 0 {
		return sharedmounts.NormalizeMounts(specMounts)
	}
	return defaultMounts
}

func sharedMountVolumeName(name string) string {
	sum := sha256.Sum256([]byte(name))
	return fmt.Sprintf("shared-mount-%s", hex.EncodeToString(sum[:6]))
}

func sharedMountEnvKey(name string) string {
	trimmed := strings.TrimSpace(name)
	upper := strings.ToUpper(trimmed)
	var b strings.Builder
	for _, r := range upper {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return fmt.Sprintf("SPRITZ_SHARED_MOUNT_%s_PATH", b.String())
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "[]"
	}
	return string(data)
}
