package router

import (
	"net/http"

	"github.com/allaboutapps/integresql/internal/api"
	"github.com/allaboutapps/integresql/internal/api/admin"
	"github.com/allaboutapps/integresql/internal/api/templates"
	"github.com/labstack/echo/v4"
	echoMiddleware "github.com/labstack/echo/v4/middleware"
)

func Init(s *api.Server) {
	s.Echo = echo.New()

	s.Echo.Debug = false
	s.Echo.HideBanner = true

	s.Echo.Pre(echoMiddleware.RemoveTrailingSlash())

	s.Echo.Use(echoMiddleware.Recover())
	s.Echo.Use(echoMiddleware.RequestID())
	s.Echo.Use(echoMiddleware.Logger())

	admin.InitRoutes(s)
	templates.InitRoutes(s)

	// enable debug endpoints only if requested
	if s.Config.DebugEndpoints {
		s.Echo.GET("/debug/*", echo.WrapHandler(http.DefaultServeMux))
	}
}
