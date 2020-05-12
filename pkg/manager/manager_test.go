package manager

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
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

	m := New(ManagerConfig{
		ManagerDatabaseConfig: DatabaseConfig{
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

	template, err := m.InitializeTemplateDatabase(ctx, hash)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	if template.Ready() {
		t.Error("template database is marked as ready")
	}
	if template.TemplateHash != hash {
		t.Errorf("template has not set correctly, got %q, want %q", template.TemplateHash, hash)
	}
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

	_, err := m.InitializeTemplateDatabase(ctxt, hash)
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
		go initTemplateDB(&wg, errs, m)
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
			if err == ErrTemplateAlreadyInitialized {
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

	template, err := m.InitializeTemplateDatabase(ctx, hash)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	populateTemplateDB(t, template)

	template, err = m.FinalizeTemplateDatabase(ctx, hash)
	if err != nil {
		t.Fatalf("failed to finalize template database: %v", err)
	}

	if !template.Ready() {
		t.Error("template database is flagged as not ready")
	}
}

func TestManagerFinalizeUntrackedTemplateDatabaseIsNotPossible(t *testing.T) {
	ctx := context.Background()

	m := testManagerFromEnv()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	db, err := sql.Open("postgres", m.config.ManagerDatabaseConfig.ConnectionString())
	if err != nil {
		t.Fatalf("failed to open connection to manager database: %v", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("failed to ping connection to manager database: %v", err)
	}

	hash := "hashinghash"
	dbName := fmt.Sprintf("%s_%s_%s", m.config.DatabasePrefix, m.config.TemplateDatabasePrefix, hash)

	if _, err := db.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", pq.QuoteIdentifier(dbName))); err != nil {
		t.Fatalf("failed to manually drop template database %q: %v", dbName, err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s OWNER %s TEMPLATE %s", pq.QuoteIdentifier(dbName), pq.QuoteIdentifier(m.config.ManagerDatabaseConfig.Username), pq.QuoteIdentifier(m.config.TemplateDatabaseTemplate))); err != nil {
		t.Fatalf("failed to manually create template database %q: %v", dbName, err)
	}

	template, err := m.FinalizeTemplateDatabase(ctx, hash)
	if err == nil {
		t.Fatalf("finalize manually created template database did work: %v", err)

		if template.Ready() {
			t.Error("template database is flagged ready even though it was never initialize by us")
		}
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

	template, err := m.InitializeTemplateDatabase(ctx, hash)
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

	if !test.Ready() {
		t.Error("test database is flagged not ready")
	}

	verifyTestDB(t, test)
}

// disabled as we were running into timing issues
// func TestManagerGetTestDatabaseTimeout(t *testing.T) {
// 	ctx := context.Background()

// 	m := testManagerFromEnv()
// 	if err := m.Initialize(ctx); err != nil {
// 		t.Fatalf("initializing manager failed: %v", err)
// 	}

// 	defer disconnectManager(t, m)

// 	hash := "hashinghash"

// 	template, err := m.InitializeTemplateDatabase(ctx, hash)
// 	if err != nil {
// 		t.Fatalf("failed to initialize template database: %v", err)
// 	}

// 	populateTemplateDB(t, template)

// 	if _, err := m.FinalizeTemplateDatabase(ctx, hash); err != nil {
// 		t.Fatalf("failed to finalize template database: %v", err)
// 	}

// 	ctxt, cancel := context.WithTimeout(ctx, 10*time.Nanosecond)
// 	defer cancel()

// 	if _, err := m.GetTestDatabase(ctxt, hash); err != context.DeadlineExceeded {
// 		t.Fatalf("received unexpected error, got %v, want %v", err, context.DeadlineExceeded)
// 	}
// }

func TestManagerFinalizeTemplateAndGetTestDatabaseConcurrently(t *testing.T) {
	ctx := context.Background()

	m := testManagerFromEnv()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	testCh := make(chan error, 1)
	go func() {
		test, err := m.GetTestDatabase(ctx, hash)
		if err != nil {
			testCh <- err
			return
		}

		if !test.Ready() {
			testCh <- errors.New("test database is flagged as not ready")
			return
		}
		if !test.Dirty() {
			testCh <- errors.New("test database is not flagged as dirty")
		}

		testCh <- nil
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

	template, err := m.InitializeTemplateDatabase(ctx, hash)
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
		go getTestDB(&wg, errs, m)
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

	m := testManagerFromEnv()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	populateTemplateDB(t, template)

	testDBCount := 5
	var errs = make(chan error, testDBCount)

	var wg sync.WaitGroup
	wg.Add(testDBCount)

	for i := 0; i < testDBCount; i++ {
		go getTestDB(&wg, errs, m)
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

	m := testManagerFromEnv()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	populateTemplateDB(t, template)

	testDBCount := 5
	var errs = make(chan error, testDBCount)

	var wg sync.WaitGroup
	wg.Add(testDBCount)

	for i := 0; i < testDBCount; i++ {
		go getTestDB(&wg, errs, m)
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

	_, err = m.InitializeTemplateDatabase(ctx, hash)
	if err != nil {
		t.Fatalf("reinitialize after discard template database should work: %v", err)
	}

	if _, err := m.FinalizeTemplateDatabase(ctx, hash); err != nil {
		t.Fatalf("finalize after discard template + reinitialize template database should work: %v", err)
	}

}

func TestManagerGetTestDatabaseReusingIDs(t *testing.T) {
	ctx := context.Background()

	cfg := DefaultManagerConfigFromEnv()
	cfg.TestDatabaseMaxPoolSize = 3
	cfg.DatabasePrefix = "pgtestpool" // ensure we don't overlap with other pools running concurrently

	m := New(cfg)
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	populateTemplateDB(t, template)

	if _, err := m.FinalizeTemplateDatabase(ctx, hash); err != nil {
		t.Fatalf("failed to finalize template database: %v", err)
	}

	seenIDs := map[int]bool{}
	for i := 0; i <= cfg.TestDatabaseMaxPoolSize*3; i++ {
		test, err := m.GetTestDatabase(ctx, hash)
		if err != nil {
			t.Fatalf("failed to get test database: %v", err)
		}

		if _, ok := seenIDs[test.ID]; ok {
			t.Errorf("received already seen test database ID %d", test.ID)
		}

		seenIDs[test.ID] = true
	}
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

func TestManagerReturnTestDatabase(t *testing.T) {
	ctx := context.Background()

	m := testManagerFromEnv()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash)
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

	if err := m.ReturnTestDatabase(ctx, hash, test.ID); err != nil {
		t.Fatalf("failed to return test database: %v", err)
	}

	originalID := test.ID

	test, err = m.GetTestDatabase(ctx, hash)
	if err != nil {
		t.Fatalf("failed to get additional test database: %v", err)
	}

	if test.ID != originalID {
		t.Fatalf("failed to reuse returned test database, got ID %d, want ID %d", test.ID, originalID)
	}
}

func TestManagerReturnUntrackedTemplateDatabase(t *testing.T) {
	ctx := context.Background()

	m := testManagerFromEnv()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash)
	if err != nil {
		t.Fatalf("failed to initialize template database: %v", err)
	}

	populateTemplateDB(t, template)

	if _, err := m.FinalizeTemplateDatabase(ctx, hash); err != nil {
		t.Fatalf("failed to finalize template database: %v", err)
	}

	db, err := sql.Open("postgres", m.config.ManagerDatabaseConfig.ConnectionString())
	if err != nil {
		t.Fatalf("failed to open connection to manager database: %v", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("failed to ping connection to manager database: %v", err)
	}

	id := 321
	dbName := fmt.Sprintf("%s_%s_%s_%d", m.config.DatabasePrefix, m.config.TestDatabasePrefix, hash, id)

	if _, err := db.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", pq.QuoteIdentifier(dbName))); err != nil {
		t.Fatalf("failed to manually drop template database %q: %v", dbName, err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s OWNER %s TEMPLATE %s", pq.QuoteIdentifier(dbName), pq.QuoteIdentifier(m.config.ManagerDatabaseConfig.Username), pq.QuoteIdentifier(template.Config.Database))); err != nil {
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

	template, err := m.InitializeTemplateDatabase(ctx, hash)
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

func TestManagerClearTrackedTestDatabases(t *testing.T) {
	ctx := context.Background()

	m := testManagerFromEnv()
	if err := m.Initialize(ctx); err != nil {
		t.Fatalf("initializing manager failed: %v", err)
	}

	defer disconnectManager(t, m)

	hash := "hashinghash"

	template, err := m.InitializeTemplateDatabase(ctx, hash)
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

	if err := m.ClearTrackedTestDatabases(hash); err != nil {
		t.Fatalf("failed to clear tracked test databases: %v", err)
	}

	test, err = m.GetTestDatabase(ctx, hash)
	if err != nil {
		t.Fatalf("failed to get test database again: %v", err)
	}

	if test.ID != originalID {
		t.Errorf("received invalid test ID, got %d, want %d", test.ID, originalID)
	}
}
