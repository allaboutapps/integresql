package manager_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/allaboutapps/integresql/pkg/db"
	"github.com/allaboutapps/integresql/pkg/manager"
	"github.com/allaboutapps/integresql/pkg/pool"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagerConnect(t *testing.T) {
	t.Parallel()

	m := testManagerFromEnv()
	if err := m.Connect(context.Background()); err != nil {
		t.Errorf("manager connection failed: %v", err)
	}

	defer disconnectManager(t, m)

	if !m.Ready() {
		t.Error("manager is not ready")
	}
}

func TestManagerConnectError(t *testing.T) {
	t.Parallel()

	m, _ := manager.New(manager.ManagerConfig{
		ManagerDatabaseConfig: db.DatabaseConfig{
			Host:     "definitelydoesnotexist",
			Port:     2345,
			Username: "definitelydoesnotexist",
			Password: "definitelydoesnotexist",
			Database: "definitelydoesnotexist",
		},
		DatabasePrefix: "pgtestpool", // ensure we don't overlap with other pools running concurrently
	})
	if err := m.Connect(context.Background()); err == nil {
		t.Error("manager connection succeeded")
	}

	if m.Ready() {
		t.Errorf("manager is ready")
	}
}

func TestManagerReconnect(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	m := testManagerFromEnv()
	if err := m.Connect(ctx); err != nil {
		t.Errorf("manager connection failed: %v", err)
	}

	defer disconnectManager(t, m)

	if !m.Ready() {
		t.Error("manager is not ready")
	}

	if err := m.Reconnect(ctx, true); err != nil {
		t.Errorf("manager reconnect failed: %v", err)
	}

	if !m.Ready() {
		t.Error("manager is not ready anymore")
	}
}

func TestManagerInitialize(t *testing.T) {
	ctx := context.Background()

	m := testManagerFromEnv()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	if !m.Ready() {
		t.Error("manager is not ready")
	}
}

func TestManagerInitializeTemplateDatabase(t *testing.T) {
	ctx := context.Background()

	m := testManagerFromEnv()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash, true /*enableDBReset */)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	assert.Equal(t, hash, template.TemplateHash)
}

func TestManagerInitializeTemplateDatabaseTimeout(t *testing.T) {
	ctx := context.Background()

	m := testManagerFromEnv()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"
	ctxt, cancel := context.WithTimeout(ctx, 10*time.Nanosecond)
	defer cancel()

	_, err := m.InitializeTemplateDatabase(ctxt, hash, true /* enableDBReset */)
	if err != context.DeadlineExceeded {
		t.Fatalf("received unexpected error, got %v, want %v", err, context.DeadlineExceeded)
	}
}

func TestManagerInitializeTemplateDatabaseConcurrently(t *testing.T) {
	ctx := context.Background()

	m := testManagerFromEnv()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	templateDBCount := 5
	var errs = make(chan error, templateDBCount)

	var wg sync.WaitGroup
	wg.Add(templateDBCount)

	for i := 0; i < templateDBCount; i++ {
		go func() {
			defer wg.Done()
			initTemplateDB(ctx, errs, m)
		}()
	}

	wg.Wait()

	var results = make([]error, 0, templateDBCount)
	for i := 0; i < templateDBCount; i++ {
		results = append(results, <-errs)
	}

	close(errs)

	success := 0
	failed := 0
	errored := 0
	for _, err := range results {
		if err == nil {
			success++
		} else {
			if err == manager.ErrTemplateAlreadyInitialized {
				failed++
			} else {
				errored++
			}
		}
	}

	if success != 1 {
		t.Errorf("invalid number of successful initializations, got %d, want %d", success, 1)
	}
	if failed != templateDBCount-1 {
		t.Errorf("invalid number of failed initializations, got %d, want %d", failed, templateDBCount-1)
	}
	if errored != 0 {
		t.Errorf("invalid number of errored initializations, got %d, want %d", errored, 0)
	}
}

func TestManagerFinalizeTemplateDatabase(t *testing.T) {
	ctx := context.Background()

	m := testManagerFromEnv()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash, true /*enableDBReset */)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	populateTemplateDB(t, template)

	template, err = m.FinalizeTemplateDatabase(ctx, hash)
	if err != nil {
		t.Fatalf("failed to finalize template database: %v", err)
	}

	if template.TemplateHash != hash {
		t.Error("invalid template hash")
	}
}

func TestManagerFinalizeUntrackedTemplateDatabaseIsNotPossible(t *testing.T) {
	ctx := context.Background()

	m, config := testManagerFromEnvWithConfig()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	db, err := sql.Open("postgres", config.ManagerDatabaseConfig.ConnectionString())
	if err != nil {
		t.Fatalf("failed to open connection to manager database: %v", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("failed to ping connection to manager database: %v", err)
	}

	hash := "hashinghash"
	dbName := fmt.Sprintf("%s_%s_%s", config.DatabasePrefix, config.TemplateDatabasePrefix, hash)

	if _, err := db.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", pq.QuoteIdentifier(dbName))); err != nil {
		t.Fatalf("failed to manually drop template database %q: %v", dbName, err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s OWNER %s TEMPLATE %s", pq.QuoteIdentifier(dbName), pq.QuoteIdentifier(config.ManagerDatabaseConfig.Username), pq.QuoteIdentifier(config.TemplateDatabaseTemplate))); err != nil {
		t.Fatalf("failed to manually create template database %q: %v", dbName, err)
	}

	_, err = m.FinalizeTemplateDatabase(ctx, hash)
	if err == nil {
		t.Fatalf("finalize manually created template database did work: %v", err)
	}
}

func TestManagerFinalizeUnknownTemplateDatabase(t *testing.T) {
	ctx := context.Background()

	m := testManagerFromEnv()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "definitelydoesnotexist"

	if _, err := m.FinalizeTemplateDatabase(ctx, hash); err == nil {
		t.Fatal("succeeded in finalizing unknown template database")
	}
}

func TestManagerGetTestDatabase(t *testing.T) {
	ctx := context.Background()

	m := testManagerFromEnv()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash, true /*enableDBReset */)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	populateTemplateDB(t, template)

	if _, err := m.FinalizeTemplateDatabase(ctx, hash); err != nil {
		t.Fatalf("failed to finalize template database: %v", err)
	}

	test, err := m.GetTestDatabase(ctx, hash)
	if err != nil {
		t.Fatalf("failed to get test database: %v", err)
	}

	verifyTestDB(t, test)
}

func TestManagerGetTestDatabaseExtendPoolOnDemand(t *testing.T) {
	ctx := context.Background()

	cfg := manager.DefaultManagerConfigFromEnv()
	cfg.TestDatabaseGetTimeout = 10 * time.Nanosecond
	// no db created initally in the background
	cfg.TestDatabaseInitialPoolSize = 0
	m, _ := testManagerWithConfig(cfg)

	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash, cfg.TestDatabaseEnableReset)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	populateTemplateDB(t, template)

	if _, err := m.FinalizeTemplateDatabase(ctx, hash); err != nil {
		t.Fatalf("failed to finalize template database: %v", err)
	}

	// get should succeed because a test DB is created on demand
	testDB, err := m.GetTestDatabase(ctx, hash)
	assert.NoError(t, err)
	assert.Equal(t, 0, testDB.ID)
}

func TestManagerFinalizeTemplateAndGetTestDatabaseConcurrently(t *testing.T) {
	ctx := context.Background()

	cfg := manager.DefaultManagerConfigFromEnv()
	cfg.TemplateFinalizeTimeout = 1 * time.Second
	m, _ := testManagerWithConfig(cfg)

	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash, cfg.TestDatabaseEnableReset)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	testCh := make(chan error, 1)
	go func() {
		_, err := m.GetTestDatabase(ctx, hash)
		testCh <- err
	}()

	populateTemplateDB(t, template)

	finalizeCh := make(chan error, 1)
	go func() {
		time.Sleep(500 * time.Millisecond)

		if _, err := m.FinalizeTemplateDatabase(ctx, hash); err != nil {
			finalizeCh <- err
		}

		finalizeCh <- nil
	}()

	testDone := false
	finalizeDone := false
	for {
		select {
		case err := <-testCh:
			if err != nil {
				t.Fatalf("failed to get test database: %v", err)
			}

			testDone = true
		case err := <-finalizeCh:
			if err != nil {
				t.Fatalf("failed to finalize template database: %v", err)
			}

			finalizeDone = true
		}

		if testDone && finalizeDone {
			break
		} else if testDone && !finalizeDone {
			t.Fatal("getting test database completed before finalizing template database")
		}
	}
}

func TestManagerGetTestDatabaseConcurrently(t *testing.T) {
	ctx := context.Background()

	m := testManagerFromEnv()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash, true /*enableDBReset */)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	populateTemplateDB(t, template)

	if _, err := m.FinalizeTemplateDatabase(ctx, hash); err != nil {
		t.Fatalf("failed to finalize template database: %v", err)
	}

	testDBCount := 5
	var errs = make(chan error, testDBCount)

	var wg sync.WaitGroup
	wg.Add(testDBCount)

	for i := 0; i < testDBCount; i++ {
		go func() {
			defer wg.Done()
			getTestDB(ctx, errs, m)
		}()
	}

	wg.Wait()

	var results = make([]error, 0, testDBCount)
	for i := 0; i < testDBCount; i++ {
		results = append(results, <-errs)
	}

	close(errs)

	success := 0
	errored := 0
	for _, err := range results {
		if err == nil {
			success++
		} else {
			errored++
		}
	}

	if success != testDBCount {
		t.Errorf("invalid number of successful retrievals, got %d, want %d", success, testDBCount)
	}
	if errored != 0 {
		t.Errorf("invalid number of errored retrievals, got %d, want %d", errored, 0)
	}
}

func TestManagerDiscardTemplateDatabase(t *testing.T) {
	ctx := context.Background()

	cfg := manager.DefaultManagerConfigFromEnv()
	cfg.TemplateFinalizeTimeout = 200 * time.Millisecond
	m, _ := testManagerWithConfig(cfg)

	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash, cfg.TestDatabaseEnableReset)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	populateTemplateDB(t, template)

	testDBCount := 5
	var errs = make(chan error, testDBCount)

	var wg sync.WaitGroup
	wg.Add(testDBCount)

	for i := 0; i < testDBCount; i++ {
		go func() {
			defer wg.Done()
			getTestDB(ctx, errs, m)
		}()
	}

	if err := m.DiscardTemplateDatabase(ctx, hash); err != nil {
		t.Fatalf("failed to kill template database: %v", err)
	}

	wg.Wait()

	var results = make([]error, 0, testDBCount)
	for i := 0; i < testDBCount; i++ {
		results = append(results, <-errs)
	}

	close(errs)

	success := 0
	errored := 0
	for _, err := range results {
		if err == nil {
			success++
		} else {
			// fmt.Println(err)
			errored++
		}
	}

	if errored != testDBCount {
		t.Errorf("invalid number of errored retrievals, got %d, want %d", errored, testDBCount)
	}

	if success != 0 {
		t.Errorf("invalid number of successful retrievals, got %d, want %d", success, 0)
	}
}

func TestManagerDiscardThenReinitializeTemplateDatabase(t *testing.T) {
	ctx := context.Background()

	cfg := manager.DefaultManagerConfigFromEnv()
	cfg.TemplateFinalizeTimeout = 200 * time.Millisecond
	m, _ := testManagerWithConfig(cfg)

	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash, cfg.TestDatabaseEnableReset)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	populateTemplateDB(t, template)

	testDBCount := 5
	var errs = make(chan error, testDBCount)

	var wg sync.WaitGroup
	wg.Add(testDBCount)

	for i := 0; i < testDBCount; i++ {
		go func() {
			defer wg.Done()
			getTestDB(ctx, errs, m)
		}()
	}

	if err := m.DiscardTemplateDatabase(ctx, hash); err != nil {
		t.Fatalf("failed to kill template database: %v", err)
	}

	wg.Wait()

	var results = make([]error, 0, testDBCount)
	for i := 0; i < testDBCount; i++ {
		results = append(results, <-errs)
	}

	close(errs)

	success := 0
	errored := 0
	for _, err := range results {
		if err == nil {
			success++
		} else {
			// fmt.Println(err)
			errored++
		}
	}

	if errored != testDBCount {
		t.Errorf("invalid number of errored retrievals, got %d, want %d", errored, testDBCount)
	}

	if success != 0 {
		t.Errorf("invalid number of successful retrievals, got %d, want %d", success, 0)
	}

	if _, err := m.FinalizeTemplateDatabase(ctx, hash); err == nil {
		t.Fatalf("finalize template should not work: %v", err)
	}

	_, err = m.InitializeTemplateDatabase(ctx, hash, cfg.TestDatabaseEnableReset)
	if err != nil {
		t.Fatalf("reinitialize after discard template database should work: %v", err)
	}

	if _, err := m.FinalizeTemplateDatabase(ctx, hash); err != nil {
		t.Fatalf("finalize after discard template + reinitialize template database should work: %v", err)
	}

}

func TestManagerGetTestDatabaseReusingIDs(t *testing.T) {
	ctx := context.Background()

	cfg := manager.DefaultManagerConfigFromEnv()
	cfg.TestDatabaseInitialPoolSize = 3
	cfg.TestDatabaseMaxPoolSize = 3
	cfg.TestDatabaseEnableReset = true
	cfg.TestDatabaseGetTimeout = 200 * time.Millisecond
	m, _ := testManagerWithConfig(cfg)

	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash, cfg.TestDatabaseEnableReset)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	populateTemplateDB(t, template)

	if _, err := m.FinalizeTemplateDatabase(ctx, hash); err != nil {
		t.Fatalf("failed to finalize template database: %v", err)
	}

	// request many more databases than initally added
	for i := 0; i <= cfg.TestDatabaseMaxPoolSize*3; i++ {
		test, err := m.GetTestDatabase(ctx, hash)
		assert.NoError(t, err)
		assert.NotEmpty(t, test)

		// return testDB after usage
		assert.NoError(t, m.ReturnTestDatabase(ctx, hash, test.ID))
	}

	// discard the template
	assert.NoError(t, m.DiscardTemplateDatabase(ctx, hash))
}

func TestManagerGetTestDatabaseExtendingPoolForceReturn(t *testing.T) {
	ctx := context.Background()

	cfg := manager.DefaultManagerConfigFromEnv()
	// there is just 1 database initially
	cfg.TestDatabaseInitialPoolSize = 1
	// should extend up to 10 on demand
	cfg.TestDatabaseMaxPoolSize = 10
	cfg.TestDatabaseGetTimeout = 10 * time.Nanosecond
	// force DB return
	cfg.TestDatabaseEnableReset = true
	m, _ := testManagerWithConfig(cfg)

	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash, cfg.TestDatabaseEnableReset)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	populateTemplateDB(t, template)

	if _, err := m.FinalizeTemplateDatabase(ctx, hash); err != nil {
		t.Fatalf("failed to finalize template database: %v", err)
	}

	seenIDs := map[int]bool{}
	for i := 0; i < cfg.TestDatabaseMaxPoolSize; i++ {
		test, err := m.GetTestDatabase(ctx, hash)
		if err != nil {
			t.Fatalf("failed to get test database: %v", err)
		}

		if _, ok := seenIDs[test.ID]; ok {
			t.Errorf("received already seen test database ID %d", test.ID)
		}

		seenIDs[test.ID] = true

		// don't return
	}

	// should not be able to extend beyond the limit
	_, err = m.GetTestDatabase(ctx, hash)
	assert.Error(t, err)

	// discard the template
	assert.NoError(t, m.DiscardTemplateDatabase(ctx, hash))
}

func TestManagerGetTestDatabaseDontReturn(t *testing.T) {

	ctx := context.Background()

	cfg := manager.DefaultManagerConfigFromEnv()
	cfg.TestDatabaseInitialPoolSize = 5
	cfg.TestDatabaseMaxPoolSize = 5
	// enable reusing old not returned databases
	cfg.TestDatabaseEnableReset = false
	m, _ := testManagerWithConfig(cfg)

	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash, cfg.TestDatabaseEnableReset /*enableDBReset */)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	populateTemplateDB(t, template)

	if _, err := m.FinalizeTemplateDatabase(ctx, hash); err != nil {
		t.Fatalf("failed to finalize template database: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < cfg.TestDatabaseMaxPoolSize*5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			testDB, err := m.GetTestDatabase(ctx, hash)
			require.NoError(t, err)
			db, err := sql.Open("postgres", testDB.Config.ConnectionString())
			assert.NoError(t, err)

			// keep an open DB connection for a while
			time.Sleep(200 * time.Millisecond)

			// now disconnect
			db.Close()
			// don't return
		}()
	}
	wg.Wait()

	// discard the template
	assert.NoError(t, m.DiscardTemplateDatabase(ctx, hash))
}

func TestManagerGetTestDatabaseForUnknownTemplate(t *testing.T) {
	ctx := context.Background()

	m := testManagerFromEnv()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	if _, err := m.GetTestDatabase(ctx, hash); err == nil {
		t.Fatal("succeeded in getting test database for unknown template")
	}
}

func TestManagerReturnResetTestDatabase(t *testing.T) {
	ctx := context.Background()

	cfg := manager.DefaultManagerConfigFromEnv()
	cfg.TestDatabaseInitialPoolSize = 10
	cfg.NumOfCleaningWorkers = 2
	cfg.TestDatabaseMaxPoolSize = 10
	cfg.TestDatabaseEnableReset = true
	cfg.TestDatabaseGetTimeout = 200 * time.Millisecond

	tests := []struct {
		name         string
		giveBackFunc func(m *manager.Manager, ctx context.Context, hash string, id int) error
		resultCheck  func(row *sql.Row, id int)
	}{
		{
			name: "Reset",
			giveBackFunc: func(m *manager.Manager, ctx context.Context, hash string, id int) error {
				return m.ResetTestDatabase(ctx, hash, id)
			},
			resultCheck: func(row *sql.Row, id int) {
				assert.NoError(t, row.Err())
				var name string
				assert.ErrorIs(t, row.Scan(&name), sql.ErrNoRows, id)
			},
		},
		{
			name: "Return",
			giveBackFunc: func(m *manager.Manager, ctx context.Context, hash string, id int) error {
				return m.ReturnTestDatabase(ctx, hash, id)
			},
			resultCheck: func(row *sql.Row, id int) {
				assert.NoError(t, row.Err(), id)
				var name string
				assert.NoError(t, row.Scan(&name), id)
				assert.Equal(t, "Snufkin", name)
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {

			m, _ := testManagerWithConfig(cfg)

			if err := m.Initialize(ctx); err != nil {
				t.Fatalf("initializing manager failed: %v", err)
			}

			defer disconnectManager(t, m)

			hash := "hashinghash"

			template, err := m.InitializeTemplateDatabase(ctx, hash, cfg.TestDatabaseEnableReset)
			if err != nil {
				t.Fatalf("failed to initialize template database: %v", err)
			}

			populateTemplateDB(t, template)

			if _, err := m.FinalizeTemplateDatabase(ctx, hash); err != nil {
				t.Fatalf("failed to finalize template database: %v", err)
			}

			for i := 0; i < cfg.TestDatabaseMaxPoolSize; i++ {
				testDB, err := m.GetTestDatabase(ctx, hash)
				assert.NoError(t, err)

				// open the connection and modify the test DB
				db, err := sql.Open("postgres", testDB.Config.ConnectionString())
				require.NoError(t, err)
				require.NoError(t, db.PingContext(ctx))

				_, err = db.ExecContext(ctx, `INSERT INTO pilots (id, "name", created_at, updated_at) VALUES ('777a1a87-5ef7-4309-8814-0f1054751177', 'Snufkin', '2023-07-13 09:44:00.548', '2023-07-13 09:44:00.548')`)
				assert.NoError(t, err, testDB.ID)
				db.Close()
			}

			_, err = m.GetTestDatabase(ctx, hash)
			assert.ErrorIs(t, err, pool.ErrPoolFull)

			// reset or return test database
			for i := 0; i < cfg.TestDatabaseMaxPoolSize; i++ {
				assert.NoError(t, tt.giveBackFunc(m, ctx, hash, i), i)
			}

			for i := 0; i < cfg.TestDatabaseMaxPoolSize; i++ {
				testDB, err := m.GetTestDatabase(ctx, hash)
				assert.NoError(t, err)

				// assert that test db can be get again
				// and that it has been cleaned up
				db, err := sql.Open("postgres", testDB.Config.ConnectionString())
				require.NoError(t, err)
				require.NoError(t, db.PingContext(ctx))

				row := db.QueryRowContext(ctx, "SELECT name FROM pilots WHERE id = '777a1a87-5ef7-4309-8814-0f1054751177'")
				tt.resultCheck(row, testDB.ID)
				db.Close()
			}
		})
	}
}

func TestManagerReturnUntrackedTemplateDatabase(t *testing.T) {
	ctx := context.Background()

	m, config := testManagerFromEnvWithConfig()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash, true /*enableDBReset */)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	populateTemplateDB(t, template)

	if _, err := m.FinalizeTemplateDatabase(ctx, hash); err != nil {
		t.Fatalf("failed to finalize template database: %v", err)
	}

	db, err := sql.Open("postgres", config.ManagerDatabaseConfig.ConnectionString())
	if err != nil {
		t.Fatalf("failed to open connection to manager database: %v", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("failed to ping connection to manager database: %v", err)
	}

	id := 321
	dbName := fmt.Sprintf("%s_%s_%s_%d", config.DatabasePrefix, config.TestDatabasePrefix, hash, id)

	if _, err := db.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", pq.QuoteIdentifier(dbName))); err != nil {
		t.Fatalf("failed to manually drop template database %q: %v", dbName, err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s OWNER %s TEMPLATE %s", pq.QuoteIdentifier(dbName), pq.QuoteIdentifier(config.ManagerDatabaseConfig.Username), pq.QuoteIdentifier(template.Config.Database))); err != nil {
		t.Fatalf("failed to manually create template database %q: %v", dbName, err)
	}

	if err := m.ReturnTestDatabase(ctx, hash, id); err != nil {
		t.Fatalf("failed to return manually created test database: %v", err)
	}
}

func TestManagerReturnUnknownTemplateDatabase(t *testing.T) {
	ctx := context.Background()

	m := testManagerFromEnv()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash, true /*enableDBReset */)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	populateTemplateDB(t, template)

	if _, err := m.FinalizeTemplateDatabase(ctx, hash); err != nil {
		t.Fatalf("failed to finalize template database: %v", err)
	}

	if err := m.ReturnTestDatabase(ctx, hash, 321); err == nil {
		t.Error("succeeded in returning unknown test database")
	}

	if err := m.ReturnTestDatabase(ctx, "definitelydoesnotexist", 0); err == nil {
		t.Error("succeeded in returning test database for unknown template")
	}
}

func TestManagerMultiFinalize(t *testing.T) {
	ctx := context.Background()

	m := testManagerFromEnv()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash, true /*enableDBReset */)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	populateTemplateDB(t, template)

	var wg sync.WaitGroup
	wg.Add(3)

	errChan := make(chan error, 3)
	finalize := func(errChan chan<- error) {
		t := t
		_, err := m.FinalizeTemplateDatabase(ctx, hash)
		if errors.Is(err, manager.ErrTemplateAlreadyInitialized) {
			errChan <- err
			return
		}
		if err != nil {
			t.Fatalf("failed to finalize template database: %v", err)
		}
	}
	go func() {
		defer wg.Done()
		finalize(errChan)
	}()
	go func() {
		defer wg.Done()
		finalize(errChan)
	}()
	go func() {
		defer wg.Done()
		finalize(errChan)
	}()

	wg.Wait()

	errCount := len(errChan)
	assert.Equal(t, 2, errCount)

}

func TestManagerClearTrackedTestDatabases(t *testing.T) {
	ctx := context.Background()

	cfg := manager.DefaultManagerConfigFromEnv()
	// there are no db added in background
	cfg.TestDatabaseInitialPoolSize = 0
	cfg.TestDatabaseEnableReset = true
	m, _ := testManagerWithConfig(cfg)

	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash, cfg.TestDatabaseEnableReset)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	populateTemplateDB(t, template)

	if _, err := m.FinalizeTemplateDatabase(ctx, hash); err != nil {
		t.Fatalf("failed to finalize template database: %v", err)
	}

	test, err := m.GetTestDatabase(ctx, hash)
	if err != nil {
		t.Fatalf("failed to get test database: %v", err)
	}

	originalID := test.ID

	// clear it twice - because why not
	if err := m.ClearTrackedTestDatabases(ctx, hash); err != nil {
		t.Fatalf("failed to clear tracked test databases: %v", err)
	}
	assert.ErrorIs(t, m.ClearTrackedTestDatabases(ctx, hash), manager.ErrTemplateNotFound)

	test, err = m.GetTestDatabase(ctx, hash)
	if err != nil {
		t.Fatalf("failed to get test database again: %v", err)
	}

	if test.ID != originalID {
		t.Errorf("received invalid test ID, got %d, want %d", test.ID, originalID)
	}
}
