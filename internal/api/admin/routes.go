package admin

import "github.com/allaboutapps/integresql/internal/api"

func InitRoutes(s *api.Server) {
	g := s.Echo.Group("/api/v1/admin")

	g.DELETE("/templates", deleteResetAllTemplates(s))
}
