package templates

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/allaboutapps/integresql/internal/api"
	"github.com/allaboutapps/integresql/pkg/manager"
	"github.com/allaboutapps/integresql/pkg/pool"
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

		template, err := s.Manager.InitializeTemplateDatabase(c.Request().Context(), payload.Hash)
		if err != nil {
			if errors.Is(err, manager.ErrManagerNotReady) {
				return echo.ErrServiceUnavailable
			} else if errors.Is(err, manager.ErrTemplateAlreadyInitialized) {
				return echo.NewHTTPError(http.StatusLocked, "template is already initialized")
			}

			// default 500
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}

		return c.JSON(http.StatusOK, &template)
	}
}

func putFinalizeTemplate(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		hash := c.Param("hash")

		if _, err := s.Manager.FinalizeTemplateDatabase(c.Request().Context(), hash); err != nil {
			if errors.Is(err, manager.ErrTemplateAlreadyInitialized) {
				// template is initialized, we ignore this error
				return c.NoContent(http.StatusNoContent)
			} else if errors.Is(err, manager.ErrManagerNotReady) {
				return echo.ErrServiceUnavailable
			} else if errors.Is(err, manager.ErrTemplateNotFound) {
				return echo.NewHTTPError(http.StatusNotFound, "template not found")
			}

			// default 500
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}

		return c.NoContent(http.StatusNoContent)
	}
}

func deleteDiscardTemplate(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		hash := c.Param("hash")

		if err := s.Manager.DiscardTemplateDatabase(c.Request().Context(), hash); err != nil {
			if errors.Is(err, manager.ErrManagerNotReady) {
				return echo.ErrServiceUnavailable
			} else if errors.Is(err, manager.ErrTemplateNotFound) {
				return echo.NewHTTPError(http.StatusNotFound, "template not found")
			}

			// default 500
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}

		return c.NoContent(http.StatusNoContent)
	}
}

func getTestDatabase(s *api.Server) echo.HandlerFunc {

	return func(c echo.Context) error {
		hash := c.Param("hash")

		test, err := s.Manager.GetTestDatabase(c.Request().Context(), hash)
		if err != nil {

			if errors.Is(err, manager.ErrManagerNotReady) {
				return echo.ErrServiceUnavailable
			} else if errors.Is(err, manager.ErrTemplateNotFound) {
				return echo.NewHTTPError(http.StatusNotFound, "template not found")
			} else if errors.Is(err, manager.ErrTemplateDiscarded) {
				return echo.NewHTTPError(http.StatusGone, "template was just discarded")
			}

			// default 500
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}

		return c.JSON(http.StatusOK, &test)
	}
}

// deprecated
func deleteReturnTestDatabase(s *api.Server) echo.HandlerFunc {
	return postUnlockTestDatabase(s)
}

func postUnlockTestDatabase(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		hash := c.Param("hash")
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid test database ID")
		}

		if err := s.Manager.ReturnTestDatabase(c.Request().Context(), hash, id); err != nil {
			if errors.Is(err, manager.ErrManagerNotReady) {
				return echo.ErrServiceUnavailable
			} else if errors.Is(err, manager.ErrTemplateNotFound) {
				return echo.NewHTTPError(http.StatusNotFound, "template not found")
			} else if errors.Is(err, manager.ErrTestNotFound) {
				return echo.NewHTTPError(http.StatusNotFound, "test database not found")
			} else if errors.Is(err, pool.ErrTestDBInUse) {
				return echo.NewHTTPError(http.StatusLocked, pool.ErrTestDBInUse.Error())
			}

			// default 500
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}

		return c.NoContent(http.StatusNoContent)
	}
}

func postRecreateTestDatabase(s *api.Server) echo.HandlerFunc {
	return func(c echo.Context) error {
		hash := c.Param("hash")
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid test database ID")
		}

		if err := s.Manager.RecreateTestDatabase(c.Request().Context(), hash, id); err != nil {

			if errors.Is(err, manager.ErrManagerNotReady) {
				return echo.ErrServiceUnavailable
			} else if errors.Is(err, manager.ErrTemplateNotFound) {
				return echo.NewHTTPError(http.StatusNotFound, "template not found")
			} else if errors.Is(err, manager.ErrTestNotFound) {
				return echo.NewHTTPError(http.StatusNotFound, "test database not found")
			} else if errors.Is(err, pool.ErrTestDBInUse) {
				return echo.NewHTTPError(http.StatusLocked, pool.ErrTestDBInUse.Error())
			}

			// default 500
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}

		return c.NoContent(http.StatusNoContent)
	}
}
