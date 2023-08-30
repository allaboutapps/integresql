package manager

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime/trace"
	"strings"

	"github.com/allaboutapps/integresql/pkg/db"
	"github.com/allaboutapps/integresql/pkg/pool"
	"github.com/allaboutapps/integresql/pkg/templates"
	"github.com/lib/pq"
)

var (
	ErrManagerNotReady            = errors.New("manager not ready")
	ErrTemplateAlreadyInitialized = errors.New("template is already initialized")
	ErrTemplateNotFound           = errors.New("template not found")
	ErrTestNotFound               = errors.New("test database not found")
	ErrTemplateDiscarded          = errors.New("template is discarded, can't be used")
	ErrInvalidTemplateState       = errors.New("unexpected template state")
)

type Manager struct {
	config ManagerConfig
	db     *sql.DB

	templates *templates.Collection
	pool      *pool.PoolCollection
}

func New(config ManagerConfig) (*Manager, ManagerConfig) {

	var testDBPrefix string
	if config.DatabasePrefix != "" {
		testDBPrefix = testDBPrefix + fmt.Sprintf("%s_", config.DatabasePrefix)
	}
	if config.PoolConfig.TestDBNamePrefix != "" {
		testDBPrefix = testDBPrefix + fmt.Sprintf("%s_", config.PoolConfig.TestDBNamePrefix)
	}

	config.PoolConfig.TestDBNamePrefix = testDBPrefix

	if len(config.TestDatabaseOwner) == 0 {
		config.TestDatabaseOwner = config.ManagerDatabaseConfig.Username
	}

	if len(config.TestDatabaseOwnerPassword) == 0 {
		config.TestDatabaseOwnerPassword = config.ManagerDatabaseConfig.Password
	}

	// at least one test database needs to be present initially
	if config.PoolConfig.InitialPoolSize == 0 {
		config.PoolConfig.InitialPoolSize = 1
	}

	if config.PoolConfig.InitialPoolSize > config.PoolConfig.MaxPoolSize && config.PoolConfig.MaxPoolSize > 0 {
		config.PoolConfig.InitialPoolSize = config.PoolConfig.MaxPoolSize
	}

	if config.PoolConfig.MaxParallelTasks < 1 {
		config.PoolConfig.MaxParallelTasks = 1
	}

	m := &Manager{
		config:    config,
		db:        nil,
		templates: templates.NewCollection(),
		pool:      pool.NewPoolCollection(config.PoolConfig),
	}

	return m, m.config
}

func DefaultFromEnv() *Manager {
	m, _ := New(DefaultManagerConfigFromEnv())
	return m
}

func (m *Manager) Connect(ctx context.Context) error {
	if m.db != nil {
		return errors.New("manager is already connected")
	}

	db, err := sql.Open("postgres", m.config.ManagerDatabaseConfig.ConnectionString())
	if err != nil {
		return err
	}

	if err := db.PingContext(ctx); err != nil {
		return err
	}

	m.db = db

	return nil
}

func (m *Manager) Disconnect(ctx context.Context, ignoreCloseError bool) error {
	if m.db == nil {
		return errors.New("manager is not connected")
	}

	// stop the pool before closing DB connection
	m.pool.Stop()

	if err := m.db.Close(); err != nil && !ignoreCloseError {
		return err
	}

	m.db = nil

	return nil
}

func (m *Manager) Reconnect(ctx context.Context, ignoreDisconnectError bool) error {
	if err := m.Disconnect(ctx, ignoreDisconnectError); err != nil && !ignoreDisconnectError {
		return err
	}

	return m.Connect(ctx)
}

func (m Manager) Ready() bool {
	return m.db != nil
}

func (m *Manager) Initialize(ctx context.Context) error {
	if !m.Ready() {
		if err := m.Connect(ctx); err != nil {
			return err
		}
	}

	rows, err := m.db.QueryContext(ctx, "SELECT datname FROM pg_database WHERE datname LIKE $1", fmt.Sprintf("%s_%s_%%", m.config.DatabasePrefix, m.config.PoolConfig.TestDBNamePrefix))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var dbName string
		if err := rows.Scan(&dbName); err != nil {
			return err
		}

		if _, err := m.db.Exec(fmt.Sprintf("DROP DATABASE %s", pq.QuoteIdentifier(dbName))); err != nil {
			return err
		}
	}

	return nil
}

func (m Manager) InitializeTemplateDatabase(ctx context.Context, hash string) (db.TemplateDatabase, error) {
	ctx, task := trace.NewTask(ctx, "initialize_template_db")
	defer task.End()

	if !m.Ready() {
		return db.TemplateDatabase{}, ErrManagerNotReady
	}

	dbName := m.makeTemplateDatabaseName(hash)
	templateConfig := templates.TemplateConfig{
		DatabaseConfig: db.DatabaseConfig{
			Host:     m.config.ManagerDatabaseConfig.Host,
			Port:     m.config.ManagerDatabaseConfig.Port,
			Username: m.config.ManagerDatabaseConfig.Username,
			Password: m.config.ManagerDatabaseConfig.Password,
			Database: dbName,
		},
	}

	added, unlock := m.templates.Push(ctx, hash, templateConfig)
	// unlock template collection only after the template is actually initalized in the DB
	defer unlock()

	if !added {
		return db.TemplateDatabase{}, ErrTemplateAlreadyInitialized
	}

	reg := trace.StartRegion(ctx, "drop_and_create_db")
	if err := m.dropAndCreateDatabase(ctx, dbName, m.config.ManagerDatabaseConfig.Username, m.config.TemplateDatabaseTemplate); err != nil {
		m.templates.RemoveUnsafe(ctx, hash)

		return db.TemplateDatabase{}, err
	}
	reg.End()

	// if template config has been overwritten, the existing pool needs to be removed
	err := m.pool.RemoveAllWithHash(ctx, hash, m.dropTestPoolDB)
	if err != nil && !errors.Is(err, pool.ErrUnknownHash) {
		m.templates.RemoveUnsafe(ctx, hash)

		return db.TemplateDatabase{}, err
	}

	return db.TemplateDatabase{
		Database: db.Database{
			TemplateHash: hash,
			Config:       templateConfig.DatabaseConfig,
		},
	}, nil
}

func (m Manager) DiscardTemplateDatabase(ctx context.Context, hash string) error {

	ctx, task := trace.NewTask(ctx, "discard_template_db")
	defer task.End()

	if !m.Ready() {
		return ErrManagerNotReady
	}

	// first remove all DB with this hash
	if err := m.pool.RemoveAllWithHash(ctx, hash, m.dropTestPoolDB); err != nil && !errors.Is(err, pool.ErrUnknownHash) {
		return err
	}

	template, found := m.templates.Pop(ctx, hash)
	dbName := template.Config.Database

	if !found {
		// even if a template is not found in the collection, it might still exist in the DB
		dbName = m.makeTemplateDatabaseName(hash)
		exists, err := m.checkDatabaseExists(ctx, dbName)
		if err != nil {
			return err
		}

		if !exists {
			return ErrTemplateNotFound
		}
	} else {
		template.SetState(ctx, templates.TemplateStateDiscarded)
	}

	return m.dropDatabase(ctx, dbName)
}

func (m Manager) FinalizeTemplateDatabase(ctx context.Context, hash string) (db.TemplateDatabase, error) {
	ctx, task := trace.NewTask(ctx, "finalize_template_db")
	defer task.End()

	if !m.Ready() {
		return db.TemplateDatabase{}, ErrManagerNotReady
	}

	template, found := m.templates.Get(ctx, hash)
	if !found {
		return db.TemplateDatabase{}, ErrTemplateNotFound
	}

	state, lockedTemplate := template.GetStateWithLock(ctx)
	defer lockedTemplate.Unlock()

	// early bailout if we are already ready (multiple calls)
	if state == templates.TemplateStateFinalized {
		return db.TemplateDatabase{Database: template.Database}, ErrTemplateAlreadyInitialized
	}

	// Disallow transition from discarded to ready
	if state == templates.TemplateStateDiscarded {
		return db.TemplateDatabase{}, ErrTemplateDiscarded
	}

	// Init a pool with this hash
	m.pool.InitHashPool(ctx, template.Database, m.recreateTestPoolDB)

	lockedTemplate.SetState(ctx, templates.TemplateStateFinalized)

	return db.TemplateDatabase{Database: template.Database}, nil
}

// GetTestDatabase tries to get a ready test DB from an existing pool.
func (m Manager) GetTestDatabase(ctx context.Context, hash string) (db.TestDatabase, error) {
	ctx, task := trace.NewTask(ctx, "get_test_db")
	defer task.End()

	if !m.Ready() {
		return db.TestDatabase{}, ErrManagerNotReady
	}

	template, found := m.templates.Get(ctx, hash)
	if !found {
		return db.TestDatabase{}, ErrTemplateNotFound
	}

	// if the template has been discarded/not initalized yet,
	// no DB should be returned, even if already in the pool
	state := template.WaitUntilFinalized(ctx, m.config.TemplateFinalizeTimeout)
	if state != templates.TemplateStateFinalized {
		return db.TestDatabase{}, ErrInvalidTemplateState
	}

	ctx, task = trace.NewTask(ctx, "get_with_timeout")
	testDB, err := m.pool.GetTestDatabase(ctx, template.TemplateHash, m.config.TestDatabaseGetTimeout)
	task.End()
	if errors.Is(err, pool.ErrUnknownHash) {
		// Template exists, but the pool is not there -
		// it must have been removed.
		// It needs to be reinitialized.
		m.pool.InitHashPool(ctx, template.Database, m.recreateTestPoolDB)

		testDB, err = m.pool.GetTestDatabase(ctx, template.TemplateHash, m.config.TestDatabaseGetTimeout)
	}

	if err != nil {
		return db.TestDatabase{}, err
	}

	return testDB, nil
}

// ReturnTestDatabase returns the given test DB directly to the pool, without cleaning (recreating it).
func (m Manager) ReturnTestDatabase(ctx context.Context, hash string, id int) error {
	ctx, task := trace.NewTask(ctx, "return_test_db")
	defer task.End()

	if !m.Ready() {
		return ErrManagerNotReady
	}

	// check if the template exists and is finalized
	template, found := m.templates.Get(ctx, hash)
	if !found {
		return ErrTemplateNotFound
	}

	if template.WaitUntilFinalized(ctx, m.config.TemplateFinalizeTimeout) !=
		templates.TemplateStateFinalized {

		return ErrInvalidTemplateState
	}

	// template is ready, we can return unchanged testDB to the pool
	if err := m.pool.ReturnTestDatabase(ctx, hash, id); err != nil {

		fmt.Printf("manager.ReturnTestDatabase error: %v\n", err)

		return err
	}

	return nil
}

// RecreateTestDatabase recreates the test DB according to the template and returns it back to the pool.
func (m *Manager) RecreateTestDatabase(ctx context.Context, hash string, id int) error {
	ctx, task := trace.NewTask(ctx, "recreate_test_db")
	defer task.End()

	if !m.Ready() {
		return ErrManagerNotReady
	}

	// check if the template exists and is finalized
	template, found := m.templates.Get(ctx, hash)
	if !found {
		return ErrTemplateNotFound
	}

	if template.WaitUntilFinalized(ctx, m.config.TemplateFinalizeTimeout) !=
		templates.TemplateStateFinalized {
		return ErrInvalidTemplateState
	}

	// template is ready, we can return the testDB to the pool and have it cleaned up
	if err := m.pool.RecreateTestDatabase(ctx, hash, id); err != nil {

		fmt.Printf("manager.RecreateTestDatabase error: %v\n", err)

		return err
	}

	return nil
}

func (m Manager) ClearTrackedTestDatabases(ctx context.Context, hash string) error {
	if !m.Ready() {
		return ErrManagerNotReady
	}

	err := m.pool.RemoveAllWithHash(ctx, hash, m.dropTestPoolDB)
	if errors.Is(err, pool.ErrUnknownHash) {
		return ErrTemplateNotFound
	}

	return err
}

func (m Manager) ResetAllTracking(ctx context.Context) error {
	if !m.Ready() {
		return ErrManagerNotReady
	}

	// remove all templates to disallow any new test DB creation from existing templates
	m.templates.RemoveAll(ctx)

	if err := m.pool.RemoveAll(ctx, m.dropTestPoolDB); err != nil {
		return err
	}

	return nil
}

func (m Manager) dropDatabaseWithID(ctx context.Context, hash string, id int) error {
	dbName := m.pool.MakeDBName(hash, id)
	exists, err := m.checkDatabaseExists(ctx, dbName)
	if err != nil {
		return err
	}

	if !exists {
		return ErrTestNotFound
	}

	return m.dropDatabase(ctx, dbName)
}

func (m Manager) checkDatabaseExists(ctx context.Context, dbName string) (bool, error) {
	var exists bool

	// fmt.Printf("SELECT 1 AS exists FROM pg_database WHERE datname = %s\n", dbName)

	if err := m.db.QueryRowContext(ctx, "SELECT 1 AS exists FROM pg_database WHERE datname = $1", dbName).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}

		return false, err
	}

	return exists, nil
}

func (m Manager) checkDatabaseConnected(ctx context.Context, dbName string) (bool, error) {

	var countConnected int

	if err := m.db.QueryRowContext(ctx, "SELECT count(pid) FROM pg_stat_activity WHERE datname = $1", dbName).Scan(&countConnected); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}

		return false, err
	}

	if countConnected > 0 {
		return true, nil
	}

	return false, nil
}

func (m Manager) createDatabase(ctx context.Context, dbName string, owner string, template string) error {

	defer trace.StartRegion(ctx, "create_db").End()

	// fmt.Printf("CREATE DATABASE %s WITH OWNER %s TEMPLATE %s\n", pq.QuoteIdentifier(dbName), pq.QuoteIdentifier(owner), pq.QuoteIdentifier(template))

	if _, err := m.db.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s WITH OWNER %s TEMPLATE %s", pq.QuoteIdentifier(dbName), pq.QuoteIdentifier(owner), pq.QuoteIdentifier(template))); err != nil {
		return err
	}

	return nil
}

func (m Manager) recreateTestPoolDB(ctx context.Context, testDB db.TestDatabase, templateName string) error {

	connected, err := m.checkDatabaseConnected(ctx, testDB.Database.Config.Database)

	if err != nil {
		return err
	}

	if connected {
		return pool.ErrTestDBInUse
	}

	return m.dropAndCreateDatabase(ctx, testDB.Database.Config.Database, m.config.TestDatabaseOwner, templateName)
}

func (m Manager) dropTestPoolDB(ctx context.Context, testDB db.TestDatabase) error {
	return m.dropDatabase(ctx, testDB.Config.Database)
}

func (m Manager) dropDatabase(ctx context.Context, dbName string) error {

	defer trace.StartRegion(ctx, "drop_db").End()

	// fmt.Printf("DROP DATABASE IF EXISTS %s\n", pq.QuoteIdentifier(dbName))

	if _, err := m.db.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", pq.QuoteIdentifier(dbName))); err != nil {
		if strings.Contains(err.Error(), "is being accessed by other users") {
			return pool.ErrTestDBInUse
		}

		return err
	}

	return nil
}

func (m Manager) dropAndCreateDatabase(ctx context.Context, dbName string, owner string, template string) error {
	if !m.Ready() {
		return ErrManagerNotReady
	}

	if err := m.dropDatabase(ctx, dbName); err != nil {
		return err
	}

	return m.createDatabase(ctx, dbName, owner, template)
}

func (m Manager) makeTemplateDatabaseName(hash string) string {
	return fmt.Sprintf("%s_%s_%s", m.config.DatabasePrefix, m.config.TemplateDatabasePrefix, hash)
}
