package main

import "strings"

type publicErrorCode string

const (
	publicErrorCodeStateInvalid        publicErrorCode = "state.invalid"
	publicErrorCodeStateExpired        publicErrorCode = "state.expired"
	publicErrorCodeAuthDenied          publicErrorCode = "auth.denied"
	publicErrorCodeAuthFailed          publicErrorCode = "auth.failed"
	publicErrorCodeIdentityUnresolved  publicErrorCode = "identity.unresolved"
	publicErrorCodeIdentityForbidden   publicErrorCode = "identity.forbidden"
	publicErrorCodeIdentityAmbiguous   publicErrorCode = "identity.ambiguous"
	publicErrorCodePolicyForbidden     publicErrorCode = "policy.forbidden"
	publicErrorCodeResolverInvalid     publicErrorCode = "resolver.invalid"
	publicErrorCodeResolverUnavailable publicErrorCode = "resolver.unavailable"
	publicErrorCodeRegistryConflict    publicErrorCode = "registry.conflict"
	publicErrorCodeRuntimeUnavailable  publicErrorCode = "runtime.unavailable"
	publicErrorCodeInternalError       publicErrorCode = "internal.error"
)

type publicErrorOperation string

const (
	publicErrorOperationSpritzCreate   publicErrorOperation = "spritz.create"
	publicErrorOperationChannelInstall publicErrorOperation = "channel.install"
)

type publicErrorAction struct {
	Type  string `json:"type,omitempty"`
	Label string `json:"label,omitempty"`
	Href  string `json:"href,omitempty"`
}

type publicError struct {
	Code        publicErrorCode      `json:"code"`
	Operation   publicErrorOperation `json:"operation"`
	Title       string               `json:"title,omitempty"`
	Message     string               `json:"message"`
	Retryable   bool                 `json:"retryable"`
	RequestID   string               `json:"requestId,omitempty"`
	Action      *publicErrorAction   `json:"action,omitempty"`
	Subject     map[string]string    `json:"subject,omitempty"`
	SafeDetails map[string]any       `json:"safeDetails,omitempty"`
}

func (e publicError) responseData() map[string]any {
	data := map[string]any{
		"message": e.Message,
		"error":   e,
	}
	if requestID := strings.TrimSpace(e.RequestID); requestID != "" {
		data["requestId"] = requestID
	}
	return data
}

func createPublicError(
	code publicErrorCode,
	message string,
	retryable bool,
	requestID string,
	subject map[string]string,
	safeDetails map[string]any,
) publicError {
	return publicError{
		Code:        code,
		Operation:   publicErrorOperationSpritzCreate,
		Message:     message,
		Retryable:   retryable,
		RequestID:   strings.TrimSpace(requestID),
		Subject:     subject,
		SafeDetails: safeDetails,
	}
}

func publicErrorMessage(payload any) string {
	switch typed := payload.(type) {
	case publicError:
		return strings.TrimSpace(typed.Message)
	case *publicError:
		if typed == nil {
			return ""
		}
		return strings.TrimSpace(typed.Message)
	case map[string]any:
		if message, ok := typed["message"].(string); ok && strings.TrimSpace(message) != "" {
			return strings.TrimSpace(message)
		}
		if nested, ok := typed["error"]; ok {
			return publicErrorMessage(nested)
		}
	case map[string]string:
		if message, ok := typed["message"]; ok && strings.TrimSpace(message) != "" {
			return strings.TrimSpace(message)
		}
	}
	return ""
}
