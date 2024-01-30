package templates

import "github.com/allaboutapps/integresql/internal/api"

func InitRoutes(s *api.Server) {
	g := s.Echo.Group("/api/v1/templates")

	g.POST("", postInitializeTemplate(s))
	g.PUT("/:hash", putFinalizeTemplate(s))
	g.DELETE("/:hash", deleteDiscardTemplate(s))
	g.GET("/:hash/tests", getTestDatabase(s))
	g.DELETE("/:hash/tests/:id", deleteReturnTestDatabase(s)) // deprecated, use POST /unlock instead

	g.POST("/:hash/tests/:id/recreate", postRecreateTestDatabase(s))
	g.POST("/:hash/tests/:id/unlock", postUnlockTestDatabase(s))

}
