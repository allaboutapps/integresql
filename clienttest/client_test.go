package clienttest

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/lib/pq"
)

func TestClientInitializeTemplate(t *testing.T) {
	ctx := context.Background()

	s, m, c := initIsolatedManager(t)
	defer disconnectManager(m)
	defer s.Close()

	if err := c.ResetAllTracking(ctx); err != nil {
		t.Fatalf("failed to reset all test pool tracking: %v", err)
	}

	hash := "hashinghash1"

	template, err := c.InitializeTemplate(ctx, hash)
	if err != nil {
		t.Fatalf("failed to initialize template: %v", err)
	}

	if len(template.Config.Database) == 0 {
		t.Error("received invalid template database config")
	}
}

func TestClientFinalzeTemplate(t *testing.T) {
	ctx := context.Background()

	s, m, c := initIsolatedManager(t)
	defer disconnectManager(m)
	defer s.Close()

	if err := c.ResetAllTracking(ctx); err != nil {
		t.Fatalf("failed to reset all test pool tracking: %v", err)
	}

	hash := "hashinghash2"

	if _, err := c.InitializeTemplate(ctx, hash); err != nil {
		t.Fatalf("failed to initialize template: %v", err)
	}

	if err := c.FinalizeTemplate(ctx, hash); err != nil {
		t.Fatalf("failed to finalize template: %v", err)
	}
}

func TestClientGetTestDatabase(t *testing.T) {
	ctx := context.Background()

	s, m, c := initIsolatedManager(t)
	defer disconnectManager(m)
	defer s.Close()
	if err := c.ResetAllTracking(ctx); err != nil {
		t.Fatalf("failed to reset all test pool tracking: %v", err)
	}

	hash := "hashinghash3"

	if _, err := c.InitializeTemplate(ctx, hash); err != nil {
		t.Fatalf("failed to initialize template: %v", err)
	}

	if err := c.FinalizeTemplate(ctx, hash); err != nil {
		t.Fatalf("failed to finalize template: %v", err)
	}

	test, err := c.GetTestDatabase(ctx, hash)
	if err != nil {
		t.Fatalf("failed to get test database: %v", err)
	}

	if test.TemplateHash != hash {
		t.Errorf("test database has invalid template hash, got %q, want %q", test.TemplateHash, hash)
	}

	db, err := sql.Open("postgres", test.Config.ConnectionString())
	if err != nil {
		t.Fatalf("failed to open test database connection: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		t.Fatalf("failed to ping test database connection: %v", err)
	}

	test2, err := c.GetTestDatabase(ctx, hash)
	if err != nil {
		t.Fatalf("failed to get second test database: %v", err)
	}

	if test2.TemplateHash != hash {
		t.Errorf("test database has invalid second template hash, got %q, want %q", test2.TemplateHash, hash)
	}

	if test2.ID == test.ID {
		t.Error("received same test database a second time without returning")
	}

	db2, err := sql.Open("postgres", test2.Config.ConnectionString())
	if err != nil {
		t.Fatalf("failed to open second test database connection: %v", err)
	}
	defer db2.Close()

	if err := db2.Ping(); err != nil {
		t.Fatalf("failed to ping second test database connection: %v", err)
	}
}

func TestClientReturnTestDatabase(t *testing.T) {
	ctx := context.Background()

	s, m, c := initIsolatedManager(t)
	defer disconnectManager(m)
	defer s.Close()

	if err := c.ResetAllTracking(ctx); err != nil {
		t.Fatalf("failed to reset all test pool tracking: %v", err)
	}

	hash := "hashinghash4"

	if _, err := c.InitializeTemplate(ctx, hash); err != nil {
		t.Fatalf("failed to initialize template: %v", err)
	}

	if err := c.FinalizeTemplate(ctx, hash); err != nil {
		t.Fatalf("failed to finalize template: %v", err)
	}

	test, err := c.GetTestDatabase(ctx, hash)
	if err != nil {
		t.Fatalf("failed to get test database: %v", err)
	}

	if test.TemplateHash != hash {
		t.Errorf("test database has invalid template hash, got %q, want %q", test.TemplateHash, hash)
	}

	if err := c.ReturnTestDatabase(ctx, hash, test.ID); err != nil {
		t.Fatalf("failed to return test database: %v", err)
	}

	test2, err := c.GetTestDatabase(ctx, hash)
	if err != nil {
		t.Fatalf("failed to get second test database: %v", err)
	}

	if test2.TemplateHash != hash {
		t.Errorf("test database has invalid second template hash, got %q, want %q", test2.TemplateHash, hash)
	}

	if test2.ID != test.ID {
		t.Errorf("received invalid test database, want %d, got %d", test.ID, test2.ID)
	}
}
