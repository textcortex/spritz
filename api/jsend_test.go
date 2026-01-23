package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestWriteJSONUsesJSendSuccess(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	payload := map[string]string{"hello": "world"}
	if err := writeJSON(c, http.StatusOK, payload); err != nil {
		t.Fatalf("writeJSON failed: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp jsendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Status != "success" {
		t.Fatalf("expected status success, got %q", resp.Status)
	}
	if resp.Data == nil {
		t.Fatalf("expected data payload")
	}
}

func TestWriteErrorUsesFailForClientErrors(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := writeError(c, http.StatusBadRequest, "bad request"); err != nil {
		t.Fatalf("writeError failed: %v", err)
	}

	var resp jsendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Status != "fail" {
		t.Fatalf("expected status fail, got %q", resp.Status)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data to be object, got %T", resp.Data)
	}
	if data["message"] != "bad request" {
		t.Fatalf("expected message to be set, got %v", data["message"])
	}
}

func TestWriteErrorUsesErrorForServerErrors(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := writeError(c, http.StatusInternalServerError, "boom"); err != nil {
		t.Fatalf("writeError failed: %v", err)
	}

	var resp jsendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Status != "error" {
		t.Fatalf("expected status error, got %q", resp.Status)
	}
	if resp.Message != "boom" {
		t.Fatalf("expected message to be boom, got %q", resp.Message)
	}
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected code %d, got %d", http.StatusInternalServerError, resp.Code)
	}
}
