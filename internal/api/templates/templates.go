package templates

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/allaboutapps/integresql/internal/api"
	"github.com/allaboutapps/integresql/pkg/manager"
	"github.com/labstack/echo/v4"
)

func postInitializeTemplate(s *api.Server) echo.HandlerFunc {
	type requestPayload struct {
		Hash string `json:"hash"`
	}

	return func(c echo.Context) error {
		var payload requestPayload

		if err := c.Bind(&payload); err != nil {
			return err
		}

		if len(payload.Hash) == 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "hash is required")
		}

		ctx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
		defer cancel()

		template, err := s.Manager.InitializeTemplateDatabase(ctx, payload.Hash)
		if err != nil {
			switch err {
			case manager.ErrManagerNotReady:
				return echo.ErrServiceUnavailable
			case manager.ErrTemplateAlreadyInitialized:
				return echo.NewHTTPError(http.StatusLocked, "template is already initialized")
			default:
				return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
			}
		}

		return c.JSON(http.StatusOK, &template)
	}
}

func putFinalizeTemplate(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		hash := c.Param("hash")

		ctx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
		defer cancel()

		if _, err := s.Manager.FinalizeTemplateDatabase(ctx, hash); err != nil {
			switch err {
			case manager.ErrManagerNotReady:
				return echo.ErrServiceUnavailable
			case manager.ErrTemplateNotFound:
				return echo.NewHTTPError(http.StatusNotFound, "template not found")
			default:
				return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
			}
		}

		return c.NoContent(http.StatusNoContent)
	}
}

func deleteDiscardTemplate(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		hash := c.Param("hash")

		ctx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
		defer cancel()

		if err := s.Manager.DiscardTemplateDatabase(ctx, hash); err != nil {
			switch err {
			case manager.ErrManagerNotReady:
				return echo.ErrServiceUnavailable
			case manager.ErrTemplateNotFound:
				return echo.NewHTTPError(http.StatusNotFound, "template not found")
			default:
				return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
			}
		}

		return c.NoContent(http.StatusNoContent)
	}
}

func getTestDatabase(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		hash := c.Param("hash")

		ctx, cancel := context.WithTimeout(c.Request().Context(), 1*time.Minute)
		defer cancel()

		test, err := s.Manager.GetTestDatabase(ctx, hash)
		if err != nil {
			switch err {
			case manager.ErrManagerNotReady:
				return echo.ErrServiceUnavailable
			case manager.ErrTemplateNotFound:
				return echo.NewHTTPError(http.StatusNotFound, "template not found")
			case manager.ErrDatabaseDiscarded:
				return echo.NewHTTPError(http.StatusGone, "template was just discarded")
			default:
				return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
			}
		}

		return c.JSON(http.StatusOK, &test)
	}
}

func deleteReturnTestDatabase(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		hash := c.Param("hash")
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid test database ID")
		}

		ctx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
		defer cancel()

		if err := s.Manager.ReturnTestDatabase(ctx, hash, id); err != nil {
			switch err {
			case manager.ErrManagerNotReady:
				return echo.ErrServiceUnavailable
			case manager.ErrTemplateNotFound:
				return echo.NewHTTPError(http.StatusNotFound, "template not found")
			case manager.ErrTestNotFound:
				return echo.NewHTTPError(http.StatusNotFound, "test database not found")
			default:
				return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
			}
		}

		return c.NoContent(http.StatusNoContent)
	}
}
