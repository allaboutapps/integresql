package router

import (
	"net/http"

	"github.com/allaboutapps/integresql/internal/api"
	"github.com/allaboutapps/integresql/internal/api/admin"
	"github.com/allaboutapps/integresql/internal/api/middleware"
	"github.com/allaboutapps/integresql/internal/api/templates"
	"github.com/labstack/echo/v4"
	echoMiddleware "github.com/labstack/echo/v4/middleware"
	"github.com/rs/zerolog/log"
)

func Init(s *api.Server) {
	s.Echo = echo.New()

	s.Echo.Debug = s.Config.Echo.Debug
	s.Echo.HideBanner = true
	s.Echo.Logger.SetOutput(&echoLogger{level: s.Config.Logger.RequestLevel, log: log.With().Str("component", "echo").Logger()})

	// ---
	// General middleware
	if s.Config.Echo.EnableTrailingSlashMiddleware {
		s.Echo.Pre(echoMiddleware.RemoveTrailingSlash())
	} else {
		log.Warn().Msg("Disabling trailing slash middleware due to environment config")
	}

	if s.Config.Echo.EnableRecoverMiddleware {
		s.Echo.Use(echoMiddleware.Recover())
	} else {
		log.Warn().Msg("Disabling recover middleware due to environment config")
	}

	if s.Config.Echo.EnableRequestIDMiddleware {
		s.Echo.Use(echoMiddleware.RequestID())
	} else {
		log.Warn().Msg("Disabling request ID middleware due to environment config")
	}

	if s.Config.Echo.EnableLoggerMiddleware {
		s.Echo.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
			Level:             s.Config.Logger.RequestLevel,
			LogRequestBody:    s.Config.Logger.LogRequestBody,
			LogRequestHeader:  s.Config.Logger.LogRequestHeader,
			LogRequestQuery:   s.Config.Logger.LogRequestQuery,
			LogResponseBody:   s.Config.Logger.LogResponseBody,
			LogResponseHeader: s.Config.Logger.LogResponseHeader,
			RequestBodyLogSkipper: func(req *http.Request) bool {
				return middleware.DefaultRequestBodyLogSkipper(req)
			},
			ResponseBodyLogSkipper: func(req *http.Request, res *echo.Response) bool {
				return middleware.DefaultResponseBodyLogSkipper(req, res)
			},
			Skipper: func(c echo.Context) bool {
				return false
			},
		}))
	} else {
		log.Warn().Msg("Disabling logger middleware due to environment config")
	}

	if s.Config.Echo.EnableTimeoutMiddleware {
		s.Echo.Use(echoMiddleware.TimeoutWithConfig(echoMiddleware.TimeoutConfig{
			Timeout: s.Config.Echo.RequestTimeout,
		}))
	}

	// enable debug endpoints only if requested
	if s.Config.DebugEndpoints {
		s.Echo.GET("/debug/*", echo.WrapHandler(http.DefaultServeMux))
	}

	admin.InitRoutes(s)
	templates.InitRoutes(s)
}
