package router_test

import (
	"testing"

	"github.com/allaboutapps/integresql/internal/api"
	"github.com/allaboutapps/integresql/internal/test"
	"github.com/stretchr/testify/require"
)

func TestPprofEnabledNoAuth(t *testing.T) {
	config := api.DefaultServerConfigFromEnv()

	// these are typically our default values, however we force set them here to ensure those are set while test execution.
	config.DebugEndpoints = true

	test.WithTestServerConfigurable(t, config, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/debug/pprof/heap/", nil, nil)
		require.Equal(t, 200, res.Result().StatusCode)

		// index
		res = test.PerformRequest(t, s, "GET", "/debug/pprof/", nil, nil)
		require.Equal(t, 301, res.Result().StatusCode)
	})
}

func TestPprofDisabled(t *testing.T) {
	config := api.DefaultServerConfigFromEnv()
	config.DebugEndpoints = false

	test.WithTestServerConfigurable(t, config, func(s *api.Server) {
		res := test.PerformRequest(t, s, "GET", "/debug/pprof/heap", nil, nil)
		require.Equal(t, 404, res.Result().StatusCode)
	})
}
