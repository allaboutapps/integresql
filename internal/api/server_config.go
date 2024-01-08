package api

import (
	"github.com/allaboutapps/integresql/pkg/util"
	"github.com/rs/zerolog"
)

type ServerConfig struct {
	Address        string
	Port           int
	DebugEndpoints bool
	Logger         LoggerConfig
	Echo           EchoConfig
}

type EchoConfig struct {
	Debug                         bool
	ListenAddress                 string
	EnableCORSMiddleware          bool
	EnableLoggerMiddleware        bool
	EnableRecoverMiddleware       bool
	EnableRequestIDMiddleware     bool
	EnableTrailingSlashMiddleware bool
}

type LoggerConfig struct {
	Level              zerolog.Level
	RequestLevel       zerolog.Level
	LogRequestBody     bool
	LogRequestHeader   bool
	LogRequestQuery    bool
	LogResponseBody    bool
	LogResponseHeader  bool
	PrettyPrintConsole bool
}

func DefaultServerConfigFromEnv() ServerConfig {
	return ServerConfig{
		Address:        util.GetEnv("INTEGRESQL_ADDRESS", ""),
		Port:           util.GetEnvAsInt("INTEGRESQL_PORT", 5000),
		DebugEndpoints: util.GetEnvAsBool("INTEGRESQL_DEBUG_ENDPOINTS", true), // https://golang.org/pkg/net/http/pprof/
		Echo: EchoConfig{
			Debug:                         util.GetEnvAsBool("INTEGRESQL_ECHO_DEBUG", false),
			EnableCORSMiddleware:          util.GetEnvAsBool("INTEGRESQL_ECHO_ENABLE_CORS_MIDDLEWARE", true),
			EnableLoggerMiddleware:        util.GetEnvAsBool("INTEGRESQL_ECHO_ENABLE_LOGGER_MIDDLEWARE", true),
			EnableRecoverMiddleware:       util.GetEnvAsBool("INTEGRESQL_ECHO_ENABLE_RECOVER_MIDDLEWARE", true),
			EnableRequestIDMiddleware:     util.GetEnvAsBool("INTEGRESQL_ECHO_ENABLE_REQUEST_ID_MIDDLEWARE", true),
			EnableTrailingSlashMiddleware: util.GetEnvAsBool("INTEGRESQL_ECHO_ENABLE_TRAILING_SLASH_MIDDLEWARE", true),
		},
		Logger: LoggerConfig{
			Level:              util.LogLevelFromString(util.GetEnv("INTEGRESQL_LOGGER_LEVEL", zerolog.InfoLevel.String())),
			RequestLevel:       util.LogLevelFromString(util.GetEnv("INTEGRESQL_LOGGER_REQUEST_LEVEL", zerolog.DebugLevel.String())),
			LogRequestBody:     util.GetEnvAsBool("INTEGRESQL_LOGGER_LOG_REQUEST_BODY", false),
			LogRequestHeader:   util.GetEnvAsBool("INTEGRESQL_LOGGER_LOG_REQUEST_HEADER", false),
			LogRequestQuery:    util.GetEnvAsBool("INTEGRESQL_LOGGER_LOG_REQUEST_QUERY", false),
			LogResponseBody:    util.GetEnvAsBool("INTEGRESQL_LOGGER_LOG_RESPONSE_BODY", false),
			LogResponseHeader:  util.GetEnvAsBool("INTEGRESQL_LOGGER_LOG_RESPONSE_HEADER", false),
			PrettyPrintConsole: util.GetEnvAsBool("INTEGRESQL_LOGGER_PRETTY_PRINT_CONSOLE", false),
		},
	}
}
