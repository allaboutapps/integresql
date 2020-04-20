package api

import "github.com/allaboutapps/integresql/pkg/util"

type ServerConfig struct {
	Address string
	Port    int
}

func DefaultServerConfigFromEnv() ServerConfig {
	return ServerConfig{
		Address: util.GetEnv("INTEGRESQL_ADDRESS", ""),
		Port:    util.GetEnvAsInt("INTEGRESQL_PORT", 5000),
	}
}
