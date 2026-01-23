package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type config struct {
	AppID               int64
	InstallationID      int64
	PrivateKeySecret    string
	PrivateKeyKey       string
	PrivateKeyNamespace string
	APIURL              string
	AllowedHosts        []string
	AnnotationKey       string
	AnnotationValue     string
	Namespace           string
}

func loadConfig() (config, error) {
	appID, err := requireInt64("SPRITZ_GITHUB_APP_ID")
	if err != nil {
		return config{}, err
	}
	installationID, err := requireInt64("SPRITZ_GITHUB_APP_INSTALLATION_ID")
	if err != nil {
		return config{}, err
	}
	secret := strings.TrimSpace(os.Getenv("SPRITZ_GITHUB_APP_PRIVATE_KEY_SECRET"))
	if secret == "" {
		return config{}, fmt.Errorf("SPRITZ_GITHUB_APP_PRIVATE_KEY_SECRET is required")
	}
	secretKey := strings.TrimSpace(os.Getenv("SPRITZ_GITHUB_APP_PRIVATE_KEY_KEY"))
	if secretKey == "" {
		secretKey = "private-key"
	}
	apiURL := strings.TrimSpace(os.Getenv("SPRITZ_GITHUB_API_URL"))
	if apiURL == "" {
		apiURL = "https://api.github.com"
	}

	privateKeyNamespace := strings.TrimSpace(os.Getenv("SPRITZ_GITHUB_APP_PRIVATE_KEY_NAMESPACE"))
	if privateKeyNamespace == "" {
		privateKeyNamespace = strings.TrimSpace(os.Getenv("POD_NAMESPACE"))
	}
	if privateKeyNamespace == "" {
		privateKeyNamespace = "default"
	}

	allowedHosts := parseHosts(os.Getenv("SPRITZ_GITHUB_ALLOWED_HOSTS"))
	if len(allowedHosts) == 0 {
		if host := apiHost(apiURL); host != "" {
			if strings.EqualFold(host, "api.github.com") {
				allowedHosts = []string{"github.com", host}
			} else {
				allowedHosts = []string{host}
			}
		}
	}

	ns := strings.TrimSpace(os.Getenv("SPRITZ_NAMESPACE"))
	annotationKey := "spritz.sh/integration.repo-auth"
	annotationValue := "github-app"

	return config{
		AppID:               appID,
		InstallationID:      installationID,
		PrivateKeySecret:    secret,
		PrivateKeyKey:       secretKey,
		PrivateKeyNamespace: privateKeyNamespace,
		APIURL:              apiURL,
		AllowedHosts:        allowedHosts,
		AnnotationKey:       annotationKey,
		AnnotationValue:     annotationValue,
		Namespace:           ns,
	}, nil
}

func parseHosts(raw string) []string {
	parts := strings.Split(raw, ",")
	hosts := make([]string, 0, len(parts))
	for _, part := range parts {
		if host := strings.TrimSpace(part); host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

func requireInt64(env string) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(env))
	if raw == "" {
		return 0, fmt.Errorf("%s is required", env)
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s value: %w", env, err)
	}
	return value, nil
}

func apiHost(apiURL string) string {
	parsed, err := url.Parse(apiURL)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
