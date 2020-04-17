package pgtestpool

import (
	"os"
	"strconv"
)

type ManagerConfig struct {
	ManagerDatabaseConfig    DatabaseConfig
	TemplateDatabaseTemplate string

	DatabasePrefix              string
	TemplateDatabasePrefix      string
	TestDatabasePrefix          string
	TestDatabaseOwner           string
	TestDatabaseOwnerPassword   string
	TestDatabaseInitialPoolSize int
	TestDatabaseMaxPoolSize     int
}

func DefaultManagerConfigFromEnv() ManagerConfig {

	return ManagerConfig{

		ManagerDatabaseConfig: DatabaseConfig{

			Host: GetEnv("INTEGRESQL_PGHOST", GetEnv("PGHOST", "127.0.0.1")),
			Port: GetEnvAsInt("INTEGRESQL_PGPORT", GetEnvAsInt("PGPORT", 5432)),

			// fallback to the current user
			Username: GetEnv("INTEGRESQL_PGUSER", GetEnv("PGUSER", GetEnv("USER", "postgres"))),
			Password: GetEnv("INTEGRESQL_PGPASSWORD", GetEnv("PGPASSWORD", "")),

			// the main db connection needs a base database that is never touched and tempered with
			// we can't use a connection to a template/test db as these dbs may be dropped/recreated
			// thus typically this should just be the default "postgres" db
			Database: GetEnv("INTEGRESQL_PGDATABASE", "postgres"),
		},

		TemplateDatabaseTemplate: GetEnv("INTEGRESQL_TEMPLATE0", "template0"),

		DatabasePrefix: GetEnv("INTEGRESQL_DBPREFIX", "integresql"),

		// DatabasePrefix_TemplateDatabasePrefix_HASH
		TemplateDatabasePrefix: GetEnv("INTEGRESQL_TEMPLATE_DBPREFIX", "template"),

		// DatabasePrefix_TestDatabasePrefix_HASH_ID
		TestDatabasePrefix: GetEnv("INTEGRESQL_TEST_DBPREFIX", "test"),

		// reuse the same user (PGUSER) and passwort (PGPASSWORT) for the test / template databases by default
		TestDatabaseOwner:           GetEnv("INTEGRESQL_TEST_PGUSER", GetEnv("INTEGRESQL_PGUSER", GetEnv("PGUSER", "postgres"))),
		TestDatabaseOwnerPassword:   GetEnv("INTEGRESQL_TEST_PGPASSWORD", GetEnv("INTEGRESQL_PGPASSWORD", GetEnv("PGPASSWORD", ""))),
		TestDatabaseInitialPoolSize: GetEnvAsInt("INTEGRESQL_TEST_INITIAL_POOL_SIZE", 10),
		TestDatabaseMaxPoolSize:     GetEnvAsInt("INTEGRESQL_TEST_MAX_POOL_SIZE", 500),
	}
}

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
