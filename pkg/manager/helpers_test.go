package manager_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/allaboutapps/integresql/pkg/db"
	"github.com/allaboutapps/integresql/pkg/manager"
	"github.com/allaboutapps/integresql/pkg/util"
)

func testManagerFromEnv() *manager.Manager {
	conf := manager.DefaultManagerConfigFromEnv()
	conf.DatabasePrefix = "pgtestpool" // ensure we don't overlap with other pools running concurrently
	m, _ := manager.New(conf)
	return m
}

func testManagerFromEnvWithConfig() (*manager.Manager, manager.ManagerConfig) {
	conf := manager.DefaultManagerConfigFromEnv()
	conf.DatabasePrefix = "pgtestpool" // ensure we don't overlap with other pools running concurrently
	return manager.New(conf)
}

// test helpers should never return errors, but are passed the *testing.T instance and fail if needed. It seems to be recommended helper functions are moved to a testing.go file...
// https://medium.com/@povilasve/go-advanced-tips-tricks-a872503ac859
// https://about.sourcegraph.com/go/advanced-testing-in-go

func disconnectManager(t *testing.T, m *manager.Manager) {

	t.Helper()
	timeout := 1 * time.Second
	ctx := context.Background()

	_, err := util.WaitWithTimeout(ctx, timeout, func(ctx context.Context) (bool, error) {
		err := m.Disconnect(ctx, true)
		return false, err
	})

	if err != nil {
		t.Errorf("received error while disconnecting manager: %v", err)
	}

}

func initTemplateDB(ctx context.Context, errs chan<- error, m *manager.Manager) {

	template, err := m.InitializeTemplateDatabase(context.Background(), "hashinghash")
	if err != nil {
		errs <- err
		return
	}

	if template.TemplateHash != "hashinghash" {
		errs <- errors.New("template database is invalid")
		return
	}

	errs <- nil
}

func populateTemplateDB(t *testing.T, template db.Database) {
	t.Helper()

	db, err := sql.Open("postgres", template.Config.ConnectionString())
	if err != nil {
		t.Fatalf("failed to open template database connection: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("failed to ping template database connection: %v", err)
	}

	if _, err := db.ExecContext(ctx, `
		CREATE EXTENSION "uuid-ossp";
		CREATE TABLE pilots (
			id uuid NOT NULL DEFAULT uuid_generate_v4(),
			"name" text NOT NULL,
			created_at timestamptz NOT NULL,
			updated_at timestamptz NULL,
			CONSTRAINT pilot_pkey PRIMARY KEY (id)
		);
		CREATE TABLE jets (
			id uuid NOT NULL DEFAULT uuid_generate_v4(),
			pilot_id uuid NOT NULL,
			age int4 NOT NULL,
			"name" text NOT NULL,
			color text NOT NULL,
			created_at timestamptz NOT NULL,
			updated_at timestamptz NULL,
			CONSTRAINT jet_pkey PRIMARY KEY (id)
		);
		ALTER TABLE jets ADD CONSTRAINT jet_pilots_fkey FOREIGN KEY (pilot_id) REFERENCES pilots(id);
	`); err != nil {
		t.Fatalf("failed to create tables in template database: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("failed to create transaction for test data in template database: %v", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO pilots (id, "name", created_at, updated_at) VALUES ('744a1a87-5ef7-4309-8814-0f1054751156', 'Mario', '2020-03-23 09:44:00.548', '2020-03-23 09:44:00.548');
		INSERT INTO pilots (id, "name", created_at, updated_at) VALUES ('20d9d155-2e95-49a2-8889-2ae975a8617e', 'Nick', '2020-03-23 09:44:00.548', '2020-03-23 09:44:00.548');
		INSERT INTO jets (id, pilot_id, age, "name", color, created_at, updated_at) VALUES ('67d9d0c7-34e5-48b0-9c7d-c6344995353c', '744a1a87-5ef7-4309-8814-0f1054751156', 26, 'F-14B', 'grey', '2020-03-23 09:44:00.000', '2020-03-23 09:44:00.000');
		INSERT INTO jets (id, pilot_id, age, "name", color, created_at, updated_at) VALUES ('facaf791-21b4-401a-bbac-67079ae4921f', '20d9d155-2e95-49a2-8889-2ae975a8617e', 27, 'F-14B', 'grey/red', '2020-03-23 09:44:00.000', '2020-03-23 09:44:00.000');
	`); err != nil {
		t.Fatalf("failed to insert test data into tables in template database: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("failed to commit transaction with test data in template database: %v", err)
	}
}

func verifyTestDB(t *testing.T, test db.TestDatabase) {
	t.Helper()

	db, err := sql.Open("postgres", test.Config.ConnectionString())
	if err != nil {
		t.Fatalf("failed to open test database connection: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("failed to ping test database connection: %v", err)
	}

	var pilotCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pilots").Scan(&pilotCount); err != nil {
		t.Fatalf("failed to query pilot test data count: %v", err)
	}

	if pilotCount != 2 {
		t.Errorf("invalid pilot test data count, got %d, want 2", pilotCount)
	}

	var jetCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM jets").Scan(&jetCount); err != nil {
		t.Fatalf("failed to query jet test data count: %v", err)
	}

	if jetCount != 2 {
		t.Errorf("invalid jet test data count, got %d, want 2", jetCount)
	}
}

func getTestDB(ctx context.Context, errs chan<- error, m *manager.Manager) {

	_, err := m.GetTestDatabase(context.Background(), "hashinghash")
	errs <- err
}
