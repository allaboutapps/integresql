package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"time"

	// #nosec G108 - pprof handlers (conditionally made available via http.DefaultServeMux within router)
	_ "net/http/pprof"

	"github.com/allaboutapps/integresql/pkg/manager"
	"github.com/allaboutapps/integresql/pkg/util"
	"github.com/labstack/echo/v4"
)

type Server struct {
	Config  ServerConfig
	Echo    *echo.Echo
	Manager *manager.Manager
}

func NewServer(config ServerConfig) *Server {
	s := &Server{
		Config:  config,
		Echo:    nil,
		Manager: nil,
	}

	return s
}

func DefaultServerFromEnv() *Server {
	return NewServer(DefaultServerConfigFromEnv())
}

func (s *Server) Ready() bool {
	return s.Echo != nil && s.Manager != nil && s.Manager.Ready()
}

func (s *Server) Start() error {
	if !s.Ready() {
		return errors.New("server is not ready")
	}

	return s.Echo.Start(net.JoinHostPort(s.Config.Address, fmt.Sprintf("%d", s.Config.Port)))
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.Manager != nil {
		if err := s.Manager.Disconnect(ctx, true); err != nil {
			log.Printf("Received error while disconnecting manager during shutdown: %v", err)
		}
	}

	return s.Echo.Shutdown(ctx)
}

func (s *Server) InitManager(ctx context.Context) error {
	m := manager.DefaultFromEnv()

	if err := util.Retry(30, 1*time.Second, func() error {
		ctxx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		return m.Initialize(ctxx)
	}); err != nil {
		return err
	}

	s.Manager = m

	return nil
}
