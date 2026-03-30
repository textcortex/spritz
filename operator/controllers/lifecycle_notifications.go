package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultLifecycleNotificationTimeout = 3 * time.Second

// LifecycleNotificationConfig controls the optional runtime lifecycle webhook.
type LifecycleNotificationConfig struct {
	URL       string
	AuthToken string
	Timeout   time.Duration
	Client    *http.Client
}

type lifecycleNotificationPayload struct {
	Namespace  string `json:"namespace"`
	InstanceID string `json:"instanceId"`
	Phase      string `json:"phase"`
}

// NewLifecycleNotificationConfigFromEnv loads lifecycle webhook settings.
func NewLifecycleNotificationConfigFromEnv() LifecycleNotificationConfig {
	return LifecycleNotificationConfig{
		URL:       strings.TrimSpace(os.Getenv("SPRITZ_LIFECYCLE_NOTIFY_URL")),
		AuthToken: strings.TrimSpace(os.Getenv("SPRITZ_LIFECYCLE_NOTIFY_AUTH_TOKEN")),
		Timeout: parseDurationEnv(
			"SPRITZ_LIFECYCLE_NOTIFY_TIMEOUT",
			defaultLifecycleNotificationTimeout,
		),
	}
}

func (c LifecycleNotificationConfig) enabled() bool {
	return strings.TrimSpace(c.URL) != ""
}

func (c LifecycleNotificationConfig) httpClient() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: c.Timeout}
}

func (c LifecycleNotificationConfig) notifyPhase(
	ctx context.Context,
	namespace, instanceID, phase string,
) error {
	if !c.enabled() {
		return nil
	}

	payload, err := json.Marshal(
		lifecycleNotificationPayload{
			Namespace:  strings.TrimSpace(namespace),
			InstanceID: strings.TrimSpace(instanceID),
			Phase:      strings.TrimSpace(phase),
		},
	)
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.URL,
		bytes.NewReader(payload),
	)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(c.AuthToken); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := c.httpClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		return nil
	}

	body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
	return fmt.Errorf(
		"lifecycle notification failed: %s %s",
		response.Status,
		strings.TrimSpace(string(body)),
	)
}
