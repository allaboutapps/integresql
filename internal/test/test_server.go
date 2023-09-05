package test

import (
	"context"
	"testing"

	"github.com/allaboutapps/integresql/internal/api"
	"github.com/allaboutapps/integresql/internal/router"
)

// WithTestServer returns a fully configured server (using the default server config).
func WithTestServer(t *testing.T, closure func(s *api.Server)) {
	t.Helper()
	defaultConfig := api.DefaultServerConfigFromEnv()
	WithTestServerConfigurable(t, defaultConfig, closure)
}

// WithTestServerConfigurable returns a fully configured server, allowing for configuration using the provided server config.
func WithTestServerConfigurable(t *testing.T, config api.ServerConfig, closure func(s *api.Server)) {
	t.Helper()
	ctx := context.Background()
	WithTestServerConfigurableContext(ctx, t, config, closure)
}

// WithTestServerConfigurableContext returns a fully configured server, allowing for configuration using the provided server config.
// The provided context will be used during setup (instead of the default background context).
func WithTestServerConfigurableContext(ctx context.Context, t *testing.T, config api.ServerConfig, closure func(s *api.Server)) {
	t.Helper()
	execClosureNewTestServer(ctx, t, config, closure)

}

// Executes closure on a new test server
func execClosureNewTestServer(ctx context.Context, t *testing.T, config api.ServerConfig, closure func(s *api.Server)) {
	t.Helper()

	// https://stackoverflow.com/questions/43424787/how-to-use-next-available-port-in-http-listenandserve
	// You may use port 0 to indicate you're not specifying an exact port but you want a free, available port selected by the system
	config.Address = ":0"

	s := api.NewServer(config)

	if err := s.InitManager(ctx); err != nil {
		t.Fatalf("failed to start manager: %v", err)
	}

	router.Init(s)

	closure(s)

	// echo is managed and should close automatically after running the test
	if err := s.Echo.Shutdown(ctx); err != nil {
		t.Fatalf("failed to shutdown server: %v", err)
	}

	if err := s.Manager.Disconnect(ctx, true); err != nil {
		t.Fatalf("failed to shutdown manager: %v", err)
	}

	// disallow any further refs to managed object after running the test
	s = nil
}
