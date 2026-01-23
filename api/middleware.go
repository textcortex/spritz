package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/labstack/echo/v4"
)

func withRequestLogging() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			err := next(c)
			duration := time.Since(start)
			req := c.Request()
			status := c.Response().Status
			fmt.Fprintf(os.Stdout, "%s %s %d %s\n", req.Method, req.URL.Path, status, duration.Round(time.Millisecond))
			return err
		}
	}
}

func withCORS(cors corsConfig) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			req := c.Request()
			res := c.Response()
			origin := req.Header.Get("Origin")
			if origin != "" && cors.isAllowedOrigin(origin) {
				if cors.allowAnyOrigin {
					res.Header().Set("Access-Control-Allow-Origin", "*")
				} else {
					res.Header().Set("Access-Control-Allow-Origin", origin)
					res.Header().Add("Vary", "Origin")
				}
				res.Header().Set("Access-Control-Allow-Methods", cors.allowMethods)
				res.Header().Set("Access-Control-Allow-Headers", cors.allowHeaders)
				if cors.allowCreds {
					res.Header().Set("Access-Control-Allow-Credentials", "true")
				}
			}

			if req.Method == http.MethodOptions {
				return c.NoContent(http.StatusNoContent)
			}

			return next(c)
		}
	}
}
