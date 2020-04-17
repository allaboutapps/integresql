package router

import (
	"net/http"
	_ "net/http/pprof"

	"github.com/allaboutapps/integresql/server/api"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func Init(s *api.Server) *echo.Echo {
	e := echo.New()

	e.Debug = false
	e.HideBanner = true

	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())
	e.Use(middleware.CORS())

	e.Pre(middleware.RemoveTrailingSlash())

	v1 := e.Group("/api/v1")

	v1Admin := v1.Group("/admin")
	v1AdminTemplates := v1Admin.Group("/templates")
	v1AdminTemplates.GET("", s.GetAdminTemplatesHandler())
	v1AdminTemplates.DELETE("", s.ResetAllTemplatesHandler())

	v1Templates := v1.Group("/templates")
	v1Templates.POST("", s.InitializeTemplateHandler())
	v1Templates.PUT("/:hash", s.FinalizeTemplateHandler())
	v1Templates.GET("/:hash/tests", s.GetTestDatabaseHandler())
	v1Templates.DELETE("/:hash/tests/:id", s.ReturnTestDatabaseHandler())

	debug := e.Group("/debug")

	debug.GET("/pprof", echo.WrapHandler(http.DefaultServeMux))
	debug.GET("/pprof/*", echo.WrapHandler(http.DefaultServeMux))

	return e
}
