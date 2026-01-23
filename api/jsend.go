package main

import "github.com/labstack/echo/v4"

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
		Status: "fail",
		Data: map[string]string{
			"message": message,
		},
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
