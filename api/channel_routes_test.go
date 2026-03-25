package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newChannelRoutesTestServer() *server {
	return &server{
		auth: authConfig{
			mode:                     authModeHeader,
			headerID:                 "X-Spritz-Principal-Id",
			headerType:               "X-Spritz-Principal-Type",
			headerScopes:             "X-Spritz-Principal-Scopes",
			headerTrustTypeAndScopes: true,
			headerDefaultType:        principalTypeService,
		},
		internalAuth: internalAuthConfig{enabled: true, token: "spritz-internal-token"},
		terminal:     terminalConfig{enabled: false},
	}
}

func TestResolveChannelRouteReturnsResolvedInstance(t *testing.T) {
	s := newChannelRoutesTestServer()
	s.extensions = extensionRegistry{
		resolvers: []configuredResolver{
			{
				id:            "channel-routing",
				extensionType: extensionTypeResolver,
				operation:     extensionOperation("channel.route.resolve"),
				match:         extensionMatchRule{},
				transport: configuredHTTPTransport{
					url: "http://resolver.example.test/channel-route",
				},
			},
		},
	}
	originalTransport := http.DefaultClient.Transport
	http.DefaultClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "http://resolver.example.test/channel-route" {
			t.Fatalf("unexpected resolver URL: %s", req.URL.String())
		}
		if got := req.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("expected JSON request, got %q", got)
		}
		var payload extensionResolverRequestEnvelope
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("failed to decode resolver request: %v", err)
		}
		if payload.Operation != extensionOperation("channel.route.resolve") {
			t.Fatalf("expected channel route resolve operation, got %q", payload.Operation)
		}
		if payload.Context.InstanceClassID != channelRouteInstanceClassID {
			t.Fatalf("expected instanceClassId=%q, got %q", channelRouteInstanceClassID, payload.Context.InstanceClassID)
		}
		if payload.Context.Namespace != "default" {
			t.Fatalf("expected default namespace to be forwarded, got %q", payload.Context.Namespace)
		}
		if payload.Principal.ID != "shared-discord-bot" {
			t.Fatalf("expected principal id to be forwarded, got %q", payload.Principal.ID)
		}
		input, ok := payload.Input.(map[string]any)
		if !ok {
			t.Fatalf("expected map input payload, got %#v", payload.Input)
		}
		if input["provider"] != "discord" {
			t.Fatalf("expected provider to be discord, got %#v", input["provider"])
		}
		if input["externalScopeType"] != "guild" {
			t.Fatalf("expected externalScopeType=guild, got %#v", input["externalScopeType"])
		}
		if input["externalTenantId"] != "123456789012345678" {
			t.Fatalf("expected externalTenantId to match, got %#v", input["externalTenantId"])
		}
		body := `{"status":"resolved","output":{"namespace":"spritz-production","instanceId":"zeno-acme","state":"ready","routeId":"route-123"}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	defer func() {
		http.DefaultClient.Transport = originalTransport
	}()

	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodPost, "/api/channel-routes/resolve", strings.NewReader(`{"requestId":"route-req-1","provider":"discord","externalScopeType":"guild","externalTenantId":"123456789012345678"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Spritz-Principal-Id", "shared-discord-bot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", scopeChannelRouteResolve)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	for _, fragment := range []string{
		`"namespace":"spritz-production"`,
		`"instanceId":"zeno-acme"`,
		`"state":"ready"`,
		`"routeId":"route-123"`,
	} {
		if !strings.Contains(rec.Body.String(), fragment) {
			t.Fatalf("expected response to contain %q, got %s", fragment, rec.Body.String())
		}
	}
}

func TestResolveChannelRouteRejectsServicePrincipalWithoutScope(t *testing.T) {
	s := newChannelRoutesTestServer()
	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodPost, "/api/channel-routes/resolve", strings.NewReader(`{"provider":"discord","externalScopeType":"guild","externalTenantId":"123456789012345678"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Spritz-Principal-Id", "shared-discord-bot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when scope is missing, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestResolveChannelRouteReturnsUnavailableWhenNoResolverMatches(t *testing.T) {
	s := newChannelRoutesTestServer()
	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodPost, "/api/channel-routes/resolve", strings.NewReader(`{"provider":"discord","externalScopeType":"guild","externalTenantId":"123456789012345678"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Spritz-Principal-Id", "shared-discord-bot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", scopeChannelRouteResolve)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when no resolver is configured, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestResolveChannelRouteAllowsAuthDisabledMode(t *testing.T) {
	s := &server{
		auth:         authConfig{mode: authModeNone},
		internalAuth: internalAuthConfig{enabled: false},
		terminal:     terminalConfig{enabled: false},
		extensions: extensionRegistry{
			resolvers: []configuredResolver{
				{
					id:            "channel-routing",
					extensionType: extensionTypeResolver,
					operation:     extensionOperationChannelRouteResolve,
					match:         extensionMatchRule{},
					transport: configuredHTTPTransport{
						url: "http://resolver.example.test/channel-route",
					},
				},
			},
		},
	}
	originalTransport := http.DefaultClient.Transport
	http.DefaultClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"status":"resolved","output":{"namespace":"spritz-staging","instanceId":"zeno-acme"}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	defer func() {
		http.DefaultClient.Transport = originalTransport
	}()

	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodPost, "/api/channel-routes/resolve", strings.NewReader(`{"provider":"discord","externalScopeType":"guild","externalTenantId":"123456789012345678"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 in auth-disabled mode, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestResolveChannelRouteFallsBackToServerNamespace(t *testing.T) {
	s := newChannelRoutesTestServer()
	s.namespace = "spritz-production"
	s.extensions = extensionRegistry{
		resolvers: []configuredResolver{
			{
				id:            "channel-routing",
				extensionType: extensionTypeResolver,
				operation:     extensionOperationChannelRouteResolve,
				match:         extensionMatchRule{},
				transport: configuredHTTPTransport{
					url: "http://resolver.example.test/channel-route",
				},
			},
		},
	}
	originalTransport := http.DefaultClient.Transport
	http.DefaultClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"status":"resolved","output":{"instanceId":"zeno-acme","state":"ready"}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	defer func() {
		http.DefaultClient.Transport = originalTransport
	}()

	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodPost, "/api/channel-routes/resolve", strings.NewReader(`{"provider":"discord","externalScopeType":"guild","externalTenantId":"123456789012345678"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Spritz-Principal-Id", "shared-discord-bot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", scopeChannelRouteResolve)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"namespace":"spritz-production"`) {
		t.Fatalf("expected fallback namespace in response, got %s", rec.Body.String())
	}
}

func TestResolveChannelRouteFallsBackToDefaultNamespace(t *testing.T) {
	s := newChannelRoutesTestServer()
	s.extensions = extensionRegistry{
		resolvers: []configuredResolver{
			{
				id:            "channel-routing",
				extensionType: extensionTypeResolver,
				operation:     extensionOperationChannelRouteResolve,
				match:         extensionMatchRule{},
				transport: configuredHTTPTransport{
					url: "http://resolver.example.test/channel-route",
				},
			},
		},
	}
	originalTransport := http.DefaultClient.Transport
	http.DefaultClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"status":"resolved","output":{"instanceId":"zeno-acme","state":"ready"}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	defer func() {
		http.DefaultClient.Transport = originalTransport
	}()

	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodPost, "/api/channel-routes/resolve", strings.NewReader(`{"provider":"discord","externalScopeType":"guild","externalTenantId":"123456789012345678"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Spritz-Principal-Id", "shared-discord-bot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", scopeChannelRouteResolve)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"namespace":"default"`) {
		t.Fatalf("expected default namespace in response, got %s", rec.Body.String())
	}
}

func TestResolveChannelRouteRejectsResolverNamespaceMismatch(t *testing.T) {
	s := newChannelRoutesTestServer()
	s.namespace = "spritz-production"
	s.extensions = extensionRegistry{
		resolvers: []configuredResolver{
			{
				id:            "channel-routing",
				extensionType: extensionTypeResolver,
				operation:     extensionOperationChannelRouteResolve,
				match:         extensionMatchRule{},
				transport: configuredHTTPTransport{
					url: "http://resolver.example.test/channel-route",
				},
			},
		},
	}
	originalTransport := http.DefaultClient.Transport
	http.DefaultClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"status":"resolved","output":{"namespace":"spritz-staging","instanceId":"zeno-acme","state":"ready"}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	defer func() {
		http.DefaultClient.Transport = originalTransport
	}()

	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodPost, "/api/channel-routes/resolve", strings.NewReader(`{"provider":"discord","externalScopeType":"guild","externalTenantId":"123456789012345678"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Spritz-Principal-Id", "shared-discord-bot")
	req.Header.Set("X-Spritz-Principal-Type", "service")
	req.Header.Set("X-Spritz-Principal-Scopes", scopeChannelRouteResolve)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"message":"resolver output namespace is invalid"`) {
		t.Fatalf("expected namespace mismatch error, got %s", rec.Body.String())
	}
}
