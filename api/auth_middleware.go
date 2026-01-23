package main

import "github.com/labstack/echo/v4"

const principalContextKey = "spritz.principal"

func (s *server) authMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			principal, err := s.auth.principal(c.Request())
			if err != nil {
				return writeAuthError(c, err)
			}
			c.Set(principalContextKey, principal)
			return next(c)
		}
	}
}

func principalFromContext(c echo.Context) (principal, bool) {
	value := c.Get(principalContextKey)
	if value == nil {
		return principal{}, false
	}
	principalValue, ok := value.(principal)
	if !ok {
		return principal{}, false
	}
	return principalValue, true
}
