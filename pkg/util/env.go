package util

import (
	"os"
	"strconv"
)

func GetEnv(key string, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}

	return defaultVal
}

func GetEnvAsInt(key string, defaultVal int) int {
	strVal := GetEnv(key, "")

	if val, err := strconv.Atoi(strVal); err == nil {
		return val
	}

	return defaultVal
}

func GetEnvAsBool(key string, defaultVal bool) bool {
	strVal := GetEnv(key, "")

	if val, err := strconv.ParseBool(strVal); err == nil {
		return val
	}

	return defaultVal
}
