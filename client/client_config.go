package client

import "os"

type ClientConfig struct {
	BaseURL    string
	APIVersion string
}

func DefaultClientConfigFromEnv() ClientConfig {
	return ClientConfig{
		BaseURL:    getEnv("INTEGRESQL_CLIENT_BASE_URL", "http://integresql:5000/api"),
		APIVersion: getEnv("INTEGRESQL_CLIENT_API_VERSION", "v1"),
	}
}

func getEnv(key string, fallback string) string {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	return v
}
