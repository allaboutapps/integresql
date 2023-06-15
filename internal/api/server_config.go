package api

import "github.com/allaboutapps/integresql/pkg/util"

type ServerConfig struct {
	Address        string
	Port           int
	DebugEndpoints bool
}

func DefaultServerConfigFromEnv() ServerConfig {
	return ServerConfig{
		Address:        util.GetEnv("INTEGRESQL_ADDRESS", ""),
		Port:           util.GetEnvAsInt("INTEGRESQL_PORT", 5000),
		DebugEndpoints: util.GetEnvAsBool("INTEGRESQL_DEBUG_ENDPOINTS", true),
	}
}
