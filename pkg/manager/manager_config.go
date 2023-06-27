package manager

import (
	"time"

	"github.com/allaboutapps/integresql/pkg/db"
	"github.com/allaboutapps/integresql/pkg/util"
)

type ManagerConfig struct {
	ManagerDatabaseConfig    db.DatabaseConfig
	TemplateDatabaseTemplate string

	DatabasePrefix              string
	TemplateDatabasePrefix      string
	TestDatabasePrefix          string
	TestDatabaseOwner           string
	TestDatabaseOwnerPassword   string
	TestDatabaseInitialPoolSize int
	TestDatabaseMaxPoolSize     int
	TemplateFinalizeTimeout     time.Duration // Time to wait for a template to transition into the 'finalized' state
	TestDatabaseGetTimeout      time.Duration // Time to wait for a ready database before extending the pool
}

func DefaultManagerConfigFromEnv() ManagerConfig {

	return ManagerConfig{

		ManagerDatabaseConfig: db.DatabaseConfig{

			Host: util.GetEnv("INTEGRESQL_PGHOST", util.GetEnv("PGHOST", "127.0.0.1")),
			Port: util.GetEnvAsInt("INTEGRESQL_PGPORT", util.GetEnvAsInt("PGPORT", 5432)),

			// fallback to the current user
			Username: util.GetEnv("INTEGRESQL_PGUSER", util.GetEnv("PGUSER", util.GetEnv("USER", "postgres"))),
			Password: util.GetEnv("INTEGRESQL_PGPASSWORD", util.GetEnv("PGPASSWORD", "")),

			// the main db connection needs a base database that is never touched and tempered with
			// we can't use a connection to a template/test db as these dbs may be dropped/recreated
			// thus typically this should just be the default "postgres" db
			Database: util.GetEnv("INTEGRESQL_PGDATABASE", "postgres"),
		},

		TemplateDatabaseTemplate: util.GetEnv("INTEGRESQL_ROOT_TEMPLATE", "template0"),

		DatabasePrefix: util.GetEnv("INTEGRESQL_DB_PREFIX", "integresql"),

		// DatabasePrefix_TemplateDatabasePrefix_HASH
		TemplateDatabasePrefix: util.GetEnv("INTEGRESQL_TEMPLATE_DB_PREFIX", "template"),

		// DatabasePrefix_TestDatabasePrefix_HASH_ID
		TestDatabasePrefix: util.GetEnv("INTEGRESQL_TEST_DB_PREFIX", "test"),

		// reuse the same user (PGUSER) and passwort (PGPASSWORT) for the test / template databases by default
		TestDatabaseOwner:           util.GetEnv("INTEGRESQL_TEST_PGUSER", util.GetEnv("INTEGRESQL_PGUSER", util.GetEnv("PGUSER", "postgres"))),
		TestDatabaseOwnerPassword:   util.GetEnv("INTEGRESQL_TEST_PGPASSWORD", util.GetEnv("INTEGRESQL_PGPASSWORD", util.GetEnv("PGPASSWORD", ""))),
		TestDatabaseInitialPoolSize: util.GetEnvAsInt("INTEGRESQL_TEST_INITIAL_POOL_SIZE", 10),
		TestDatabaseMaxPoolSize:     util.GetEnvAsInt("INTEGRESQL_TEST_MAX_POOL_SIZE", 500),
		TemplateFinalizeTimeout:     time.Millisecond * time.Duration(util.GetEnvAsInt("INTEGRESQL_TEMPLATE_FINALIZE_TIMEOUT_MS", 100)),
		TestDatabaseGetTimeout:      time.Millisecond * time.Duration(util.GetEnvAsInt("INTEGRESQL_TEST_DB_GET_TIMEOUT_MS", 500)),
	}
}
