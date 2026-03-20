package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	spritzv1 "spritz.sh/operator/api/v1"
)

type instanceProxyConfig struct {
	enabled     bool
	stripPrefix bool
}

func newInstanceProxyConfig() instanceProxyConfig {
	return instanceProxyConfig{
		enabled:     parseBoolEnv("SPRITZ_INSTANCE_PROXY_ENABLED", true),
		stripPrefix: parseBoolEnv("SPRITZ_INSTANCE_PROXY_STRIP_PREFIX", true),
	}
}

func spritzRouteModelFromEnv() spritzv1.SharedHostRouteModel {
	return spritzv1.SharedHostRouteModelFromEnv()
}

func (c instanceProxyConfig) pathPrefix(routeModel spritzv1.SharedHostRouteModel) string {
	return routeModel.InstancePathPrefix
}

func (s *server) proxyInstanceWeb(c echo.Context) error {
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}

	namespace := s.requestNamespace(c)
	if namespace == "" {
		namespace = "default"
	}
	spritz, err := s.getAuthorizedSpritz(c.Request().Context(), principal, namespace, c.Param("name"))
	if err != nil {
		return s.writeInstanceProxyError(c, err)
	}

	target, err := s.resolveInstanceProxyTarget(spritz)
	if err != nil {
		return writeError(c, http.StatusBadGateway, err.Error())
	}

	prefix := s.instancePrefixForRequest(spritz.Name)
	proxy := s.newInstanceReverseProxy(target, prefix)
	proxy.ServeHTTP(c.Response(), c.Request())
	return nil
}

func (s *server) resolveInstanceProxyTarget(spritz *spritzv1.Spritz) (*url.URL, error) {
	if s.instanceProxyTargetResolver != nil {
		return s.instanceProxyTargetResolver(spritz)
	}
	rawURL := spritzv1.WebServiceURLForSpritz(spritz)
	if strings.TrimSpace(rawURL) == "" {
		return nil, fmt.Errorf("instance web target unavailable")
	}
	target, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	return target, nil
}

func (s *server) instancePrefixForRequest(name string) string {
	return s.routeModel.InstancePath(name)
}

func (s *server) newInstanceReverseProxy(target *url.URL, externalPrefix string) *httputil.ReverseProxy {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(proxyReq *httputil.ProxyRequest) {
			proxyReq.SetURL(target)
			proxyReq.SetXForwarded()
			req := proxyReq.In
			proxyReq.Out.Host = target.Host
			proxyReq.Out.URL.Path = s.rewriteInstanceProxyPath(req.URL.Path, externalPrefix)
			proxyReq.Out.URL.RawPath = ""
			proxyReq.Out.URL.RawQuery = req.URL.RawQuery
			proxyReq.Out.Header.Set("X-Forwarded-Host", requestForwardedHost(req))
			proxyReq.Out.Header.Set("X-Forwarded-Proto", requestForwardedProto(req))
			proxyReq.Out.Header.Set("X-Forwarded-Prefix", externalPrefix)
			stripBrowserAuthHeaders(proxyReq.Out.Header, s.auth)
		},
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
			http.Error(rw, err.Error(), http.StatusBadGateway)
		},
	}
	if s.instanceProxyTransport != nil {
		proxy.Transport = s.instanceProxyTransport
	}
	return proxy
}

func (s *server) rewriteInstanceProxyPath(requestPath, externalPrefix string) string {
	if !s.instanceProxy.stripPrefix {
		if requestPath == "" {
			return "/"
		}
		return requestPath
	}
	trimmed := strings.TrimPrefix(requestPath, externalPrefix)
	if trimmed == "" {
		return "/"
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "/" + trimmed
	}
	return trimmed
}

func stripBrowserAuthHeaders(headers http.Header, auth authConfig) {
	headers.Del("Authorization")
	headers.Del("X-Auth-Request-User")
	headers.Del("X-Auth-Request-Email")
	headers.Del("X-Auth-Request-Groups")
	headers.Del("X-Auth-Request-Access-Token")
	headers.Del("X-Forwarded-Access-Token")
	for _, name := range []string{
		auth.headerID,
		auth.headerEmail,
		auth.headerTeams,
		auth.headerType,
		auth.headerScopes,
	} {
		if strings.TrimSpace(name) != "" {
			headers.Del(name)
		}
	}
}

func requestForwardedHost(req *http.Request) string {
	if forwarded := strings.TrimSpace(req.Header.Get("X-Forwarded-Host")); forwarded != "" {
		return forwarded
	}
	return req.Host
}

func requestForwardedProto(req *http.Request) string {
	if forwarded := strings.TrimSpace(req.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		return forwarded
	}
	if req.TLS != nil {
		return "https"
	}
	if forwarded := strings.TrimSpace(req.Header.Get(echo.HeaderXForwardedProto)); forwarded != "" {
		return forwarded
	}
	return "http"
}

func (s *server) writeInstanceProxyError(c echo.Context, err error) error {
	switch {
	case apierrors.IsNotFound(err):
		return writeError(c, http.StatusNotFound, "spritz not found")
	case errors.Is(err, errForbidden):
		return writeError(c, http.StatusForbidden, "forbidden")
	default:
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
}
