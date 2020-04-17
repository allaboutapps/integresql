package api

import (
	"net/http"

	"github.com/labstack/echo/v4"
)


func (s *Server) GetAdminTemplatesHandler() echo.HandlerFunc {
	return func(c echo.Context) error {
		return echo.NewHTTPError(http.StatusNotImplemented, http.StatusText(http.StatusNotImplemented))
	}
}

func (s *Server) ResetAllTemplatesHandler() echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := s.M.ResetAllTracking(); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}

		return c.NoContent(http.StatusNoContent)
	}
}
