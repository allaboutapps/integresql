package manager

import (
	"runtime"
	"time"

	"github.com/allaboutapps/integresql/pkg/db"
	"github.com/allaboutapps/integresql/pkg/pool"
	"github.com/allaboutapps/integresql/pkg/util"
)

// we explicitly want to access this struct via manager.ManagerConfig, thus we disable revive for the next line
type ManagerConfig struct { //nolint:revive
	ManagerDatabaseConfig    db.DatabaseConfig `json:"-"` // sensitive
	TemplateDatabaseTemplate string

	DatabasePrefix            string
	TemplateDatabasePrefix    string
	TestDatabaseOwner         string
	TestDatabaseOwnerPassword string        `json:"-"` // sensitive
	TemplateFinalizeTimeout   time.Duration // Time to wait for a template to transition into the 'finalized' state
	TestDatabaseGetTimeout    time.Duration // Time to wait for a ready database

	PoolConfig pool.PoolConfig
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

		// we reuse the same user (PGUSER) and passwort (PGPASSWORT) for the test / template databases by default
		TestDatabaseOwner:         util.GetEnv("INTEGRESQL_TEST_PGUSER", util.GetEnv("INTEGRESQL_PGUSER", util.GetEnv("PGUSER", "postgres"))),
		TestDatabaseOwnerPassword: util.GetEnv("INTEGRESQL_TEST_PGPASSWORD", util.GetEnv("INTEGRESQL_PGPASSWORD", util.GetEnv("PGPASSWORD", ""))),
		TemplateFinalizeTimeout:   time.Millisecond * time.Duration(util.GetEnvAsInt("INTEGRESQL_TEMPLATE_FINALIZE_TIMEOUT_MS", 5*60*1000 /*5 min*/)),
		TestDatabaseGetTimeout:    time.Millisecond * time.Duration(util.GetEnvAsInt("INTEGRESQL_TEST_DB_GET_TIMEOUT_MS", 1*60*1000 /*1 min, timeout hardcoded also in GET request handler*/)),

		PoolConfig: pool.PoolConfig{
			InitialPoolSize:                   util.GetEnvAsInt("INTEGRESQL_TEST_INITIAL_POOL_SIZE", runtime.NumCPU()), // previously default 10
			MaxPoolSize:                       util.GetEnvAsInt("INTEGRESQL_TEST_MAX_POOL_SIZE", runtime.NumCPU()*4),   // previously default 500
			TestDBNamePrefix:                  util.GetEnv("INTEGRESQL_TEST_DB_PREFIX", "test"),                        // DatabasePrefix_TestDBNamePrefix_HASH_ID
			MaxParallelTasks:                  util.GetEnvAsInt("INTEGRESQL_POOL_MAX_PARALLEL_TASKS", runtime.NumCPU()),
			TestDatabaseRetryRecreateSleepMin: time.Millisecond * time.Duration(util.GetEnvAsInt("INTEGRESQL_TEST_DB_RETRY_RECREATE_SLEEP_MIN_MS", 250 /*250 ms*/)),
			TestDatabaseRetryRecreateSleepMax: time.Millisecond * time.Duration(util.GetEnvAsInt("INTEGRESQL_TEST_DB_RETRY_RECREATE_SLEEP_MAX_MS", 1000*3 /*3 sec*/)),
			TestDatabaseMinimalLifetime:       time.Millisecond * time.Duration(util.GetEnvAsInt("INTEGRESQL_TEST_DB_MINIMAL_LIFETIME_MS", 250 /*250 ms*/)),
		},
	}
}
