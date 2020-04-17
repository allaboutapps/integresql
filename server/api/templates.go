package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/allaboutapps/integresql/pgtestpool"
	"github.com/labstack/echo/v4"
)

type initializeTemplatePayload struct {
	Hash string `json:"hash"`
}

// swagger:route GET /templates templates init initializeTemplateHandler
//
// Summary of initializeTemplateHandler
//
// Description of initializeTemplateHandler
//
//     Produces:
//     - application/json
//
//     Schemes: http, https, ws, wss
//
//     Deprecated: false
//
//     Responses:
//       200: SomeSampleType
func (s *Server) InitializeTemplateHandler() echo.HandlerFunc {
	return func(c echo.Context) error {
		payload := new(initializeTemplatePayload)

		if err := c.Bind(payload); err != nil {
			return err
		}

		if len(payload.Hash) == 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "hash is required")
		}

		ctx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
		defer cancel()

		template, err := s.M.InitializeTemplateDatabase(ctx, payload.Hash)
		if err != nil {
			switch err {
			case pgtestpool.ErrManagerNotReady:
				return echo.ErrServiceUnavailable
			case pgtestpool.ErrTemplateAlreadyInitialized:
				return echo.NewHTTPError(http.StatusLocked, "template is already initialized")
			default:
				return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
			}
		}

		return c.JSON(http.StatusOK, &template)
	}
}

func (s *Server) FinalizeTemplateHandler() echo.HandlerFunc {
	return func(c echo.Context) error {
		hash := c.Param("hash")

		ctx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
		defer cancel()

		if _, err := s.M.FinalizeTemplateDatabase(ctx, hash); err != nil {
			switch err {
			case pgtestpool.ErrManagerNotReady:
				return echo.ErrServiceUnavailable
			case pgtestpool.ErrTemplateNotFound:
				return echo.NewHTTPError(http.StatusNotFound, "template not found")
			default:
				return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
			}
		}

		return c.NoContent(http.StatusNoContent)
	}
}

func (s *Server) GetTestDatabaseHandler() echo.HandlerFunc {
	return func(c echo.Context) error {
		hash := c.Param("hash")

		ctx, cancel := context.WithTimeout(c.Request().Context(), 1*time.Minute)
		defer cancel()

		test, err := s.M.GetTestDatabase(ctx, hash)
		if err != nil {
			switch err {
			case pgtestpool.ErrManagerNotReady:
				return echo.ErrServiceUnavailable
			case pgtestpool.ErrTemplateNotFound:
				return echo.NewHTTPError(http.StatusNotFound, "template not found")
			default:
				return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
			}
		}

		return c.JSON(http.StatusOK, &test)
	}
}

func (s *Server) ReturnTestDatabaseHandler() echo.HandlerFunc {
	return func(c echo.Context) error {
		hash := c.Param("hash")
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid test database ID")
		}

		ctx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
		defer cancel()

		if err := s.M.ReturnTestDatabase(ctx, hash, id); err != nil {
			switch err {
			case pgtestpool.ErrManagerNotReady:
				return echo.ErrServiceUnavailable
			case pgtestpool.ErrTemplateNotFound:
				return echo.NewHTTPError(http.StatusNotFound, "template not found")
			case pgtestpool.ErrTestNotFound:
				return echo.NewHTTPError(http.StatusNotFound, "test database not found")
			default:
				return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
			}
		}

		return c.NoContent(http.StatusNoContent)
	}
}
