package admin

import (
	"net/http"

	"github.com/allaboutapps/integresql/internal/api"
	"github.com/labstack/echo/v4"
)

func deleteResetAllTemplates(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := s.Manager.ResetAllTracking(); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}

		return c.NoContent(http.StatusNoContent)
	}
}
