package main

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

type jsendResponse struct {
	Status  string `json:"status"`
	Data    any    `json:"data,omitempty"`
	Message string `json:"message,omitempty"`
	Code    int    `json:"code,omitempty"`
}

func writeJSendSuccess(c echo.Context, status int, payload any) error {
	return c.JSON(status, jsendResponse{
		Status: "success",
		Data:   payload,
	})
}

func writeJSendFail(c echo.Context, status int, message string) error {
	return c.JSON(status, jsendResponse{
		Status:  "fail",
		Message: message,
		Data: map[string]string{
			"message": message,
		},
	})
}

func writeJSendFailData(c echo.Context, status int, payload any) error {
	message := jsendErrorMessage(payload, status)
	if status >= 500 {
		return c.JSON(status, jsendResponse{
			Status:  "error",
			Message: message,
			Code:    status,
			Data:    payload,
		})
	}
	return c.JSON(status, jsendResponse{
		Status:  "fail",
		Message: message,
		Data:    payload,
	})
}

func writeError(c echo.Context, status int, message string) error {
	if status >= 500 {
		return c.JSON(status, jsendResponse{
			Status:  "error",
			Message: message,
			Code:    status,
		})
	}
	return writeJSendFail(c, status, message)
}

func jsendErrorMessage(payload any, status int) string {
	if message := publicErrorMessage(payload); message != "" {
		return message
	}
	if data, ok := payload.(map[string]any); ok {
		if message, ok := data["message"].(string); ok && message != "" {
			return message
		}
	}
	if data, ok := payload.(map[string]string); ok {
		if message, ok := data["message"]; ok && message != "" {
			return message
		}
	}
	if message := http.StatusText(status); message != "" {
		return message
	}
	return "internal server error"
}
