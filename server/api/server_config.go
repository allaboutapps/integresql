package api

import (
	"os"
	"strconv"
)

type ServerConfig struct {
	Port int
}

func DefaultServerConfigFromEnv() ServerConfig {
	return ServerConfig{
		Port: getEnvAsInt("INTEGRESQL_PORT", 5000),
	}
}

func getEnv(key string, fallback string) string {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	return v
}

// https://dev.to/craicoverflow/a-no-nonsense-guide-to-environment-variables-in-go-a2f
// Simple helper function to read an environment variable into integer or return a default value
func getEnvAsInt(name string, defaultVal int) int {
	valueStr := getEnv(name, "")
	if value, err := strconv.Atoi(valueStr); err == nil {
		return value
	}

	return defaultVal
}
