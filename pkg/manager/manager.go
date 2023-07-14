package manager

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime/trace"
	"strings"
	"sync"

	"github.com/allaboutapps/integresql/pkg/db"
	"github.com/allaboutapps/integresql/pkg/pool"
	"github.com/allaboutapps/integresql/pkg/templates"
	"github.com/allaboutapps/integresql/pkg/util"
	"github.com/lib/pq"
)

var (
	ErrManagerNotReady            = errors.New("manager not ready")
	ErrTemplateAlreadyInitialized = errors.New("template is already initialized")
	ErrTemplateNotFound           = errors.New("template not found")
	ErrTestNotFound               = errors.New("test database not found")
	ErrTemplateDiscarded          = errors.New("template is discarded, can't be used")
	ErrInvalidTemplateState       = errors.New("unexpected template state")
	ErrTestDBInUse                = errors.New("test database is in use, close the connection before dropping")
)

type Manager struct {
	config ManagerConfig
	db     *sql.DB
	wg     sync.WaitGroup

	templates *templates.Collection
	pool      *pool.PoolCollection

	connectionCtx       context.Context // DB connection context used for adding initial DBs in background
	cancelConnectionCtx func()          // Cancel function for DB connection context
}

func New(config ManagerConfig) (*Manager, ManagerConfig) {

	var testDBPrefix string
	if config.DatabasePrefix != "" {
		testDBPrefix = testDBPrefix + fmt.Sprintf("%s_", config.DatabasePrefix)
	}
	if config.TestDatabasePrefix != "" {
		testDBPrefix = testDBPrefix + fmt.Sprintf("%s_", config.TestDatabasePrefix)
	}

	m := &Manager{
		config:    config,
		db:        nil,
		wg:        sync.WaitGroup{},
		templates: templates.NewCollection(),
		pool: pool.NewPoolCollection(
			pool.PoolConfig{
				MaxPoolSize:      config.TestDatabaseMaxPoolSize,
				TestDBNamePrefix: testDBPrefix,
				NumOfWorkers:     config.NumOfCleaningWorkers,
				ForceDBReturn:    config.TestDatabaseForceReturn,
			},
		),
		connectionCtx: context.TODO(),
	}

	if len(m.config.TestDatabaseOwner) == 0 {
		m.config.TestDatabaseOwner = m.config.ManagerDatabaseConfig.Username
	}

	if len(m.config.TestDatabaseOwnerPassword) == 0 {
		m.config.TestDatabaseOwnerPassword = m.config.ManagerDatabaseConfig.Password
	}

	if m.config.TestDatabaseInitialPoolSize > m.config.TestDatabaseMaxPoolSize && m.config.TestDatabaseMaxPoolSize > 0 {
		m.config.TestDatabaseInitialPoolSize = m.config.TestDatabaseMaxPoolSize
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

	// set cancellable connection context
	// used to stop background tasks
	ctx, cancel := context.WithCancel(context.Background())
	m.connectionCtx = ctx
	m.cancelConnectionCtx = cancel

	return nil
}

func (m *Manager) Disconnect(ctx context.Context, ignoreCloseError bool) error {
	if m.db == nil {
		return errors.New("manager is not connected")
	}

	// signal stop to background routines
	m.cancelConnectionCtx()
	m.connectionCtx = context.TODO()

	_, err := util.WaitWithCancellableCtx(ctx, func(context.Context) (bool, error) {
		m.wg.Wait()
		return true, nil
	})

	if err != nil {
		// we didn't manage to stop on time background routines
		// but we will continue and close the DB connection
		// TODO anna: error handling
		fmt.Println("integresql: timeout when stopping background tasks")
	}

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

func (m *Manager) Ready() bool {
	return m.db != nil
}

func (m *Manager) Initialize(ctx context.Context) error {
	if !m.Ready() {
		if err := m.Connect(ctx); err != nil {
			return err
		}
	}

	rows, err := m.db.QueryContext(ctx, "SELECT datname FROM pg_database WHERE datname LIKE $1", fmt.Sprintf("%s_%s_%%", m.config.DatabasePrefix, m.config.TestDatabasePrefix))
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

func (m *Manager) InitializeTemplateDatabase(ctx context.Context, hash string) (db.TemplateDatabase, error) {
	ctx, task := trace.NewTask(ctx, "initialize_template_db")
	defer task.End()

	if !m.Ready() {
		return db.TemplateDatabase{}, ErrManagerNotReady
	}

	dbName := m.makeTemplateDatabaseName(hash)
	templateConfig := db.DatabaseConfig{
		Host:     m.config.ManagerDatabaseConfig.Host,
		Port:     m.config.ManagerDatabaseConfig.Port,
		Username: m.config.ManagerDatabaseConfig.Username,
		Password: m.config.ManagerDatabaseConfig.Password,
		Database: dbName,
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

	return db.TemplateDatabase{
		Database: db.Database{
			TemplateHash: hash,
			Config:       templateConfig,
		},
	}, nil
}

func (m *Manager) DiscardTemplateDatabase(ctx context.Context, hash string) error {

	ctx, task := trace.NewTask(ctx, "discard_template_db")
	defer task.End()

	if !m.Ready() {
		return ErrManagerNotReady
	}

	m.wg.Wait()

	// first remove all DB with this hash
	if err := m.pool.RemoveAllWithHash(ctx, hash, func(testDB db.TestDatabase) error {
		return m.dropDatabase(ctx, testDB.Database.Config.Database)
	}); err != nil && !errors.Is(err, pool.ErrUnknownHash) {
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

func (m *Manager) FinalizeTemplateDatabase(ctx context.Context, hash string) (db.TemplateDatabase, error) {
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
	m.pool.InitHashPool(ctx, template.Database, m.recreateTestDB)

	lockedTemplate.SetState(ctx, templates.TemplateStateFinalized)
	m.addInitialTestDatabasesInBackground(template, m.config.TestDatabaseInitialPoolSize)

	return db.TemplateDatabase{Database: template.Database}, nil
}

// GetTestDatabase tries to get a ready test DB from an existing pool.
// If no DB is ready after the preconfigured timeout, it tries to extend the pool and therefore create a new DB.
func (m *Manager) GetTestDatabase(ctx context.Context, hash string) (db.TestDatabase, error) {
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

	if errors.Is(err, pool.ErrTimeout) {
		// on timeout we can try to extend the pool
		ctx, task := trace.NewTask(ctx, "extend_pool_on_demand")
		testDB, err = m.pool.ExtendPool(ctx, template.Database)
		task.End()

	} else if errors.Is(err, pool.ErrUnknownHash) {
		// Template exists, but the pool is not there -
		// it must have been removed.
		// It needs to be reinitialized.
		m.pool.InitHashPool(ctx, template.Database, m.recreateTestDB)

		// pool initalized, create one test db
		testDB, err = m.pool.ExtendPool(ctx, template.Database)
		// // and add new test DBs in the background
		// m.addInitialTestDatabasesInBackground(template, m.config.TestDatabaseInitialPoolSize)

	}

	if err != nil {
		return db.TestDatabase{}, err
	}

	if !m.config.TestDatabaseForceReturn {
		// before returning create a new test database in background
		m.wg.Add(1)
		go func(ctx context.Context, templ *templates.Template) {
			defer m.wg.Done()
			if err := m.createTestDatabaseFromTemplate(ctx, templ); err != nil {
				fmt.Printf("integresql: failed to create a new DB in background: %v\n", err)
			}
		}(m.connectionCtx, template)
	}

	return testDB, nil
}

// ReturnTestDatabase returns an unchanged test DB to the pool, allowing for reuse without cleaning.
func (m *Manager) ReturnTestDatabase(ctx context.Context, hash string, id int) error {
	ctx, task := trace.NewTask(ctx, "return_test_db")
	defer task.End()

	if !m.Ready() {
		return ErrManagerNotReady
	}

	// check if the template exists and is finalized
	template, found := m.templates.Get(ctx, hash)
	if !found {
		return m.dropDatabaseWithID(ctx, hash, id)
	}

	if template.WaitUntilFinalized(ctx, m.config.TemplateFinalizeTimeout) !=
		templates.TemplateStateFinalized {

		return ErrInvalidTemplateState
	}

	// template is ready, we can return unchanged testDB to the pool
	if err := m.pool.ReturnTestDatabase(ctx, hash, id); err != nil {
		if !(errors.Is(err, pool.ErrInvalidIndex) ||
			errors.Is(err, pool.ErrUnknownHash)) {
			// other error is an internal error
			return err
		}

		// db is not tracked in the pool
		// try to drop it if exists
		return m.dropDatabaseWithID(ctx, hash, id)
	}

	return nil
}

// RestoreTestDatabase recreates the test DB according to the template and returns it back to the pool.
func (m *Manager) RestoreTestDatabase(ctx context.Context, hash string, id int) error {
	ctx, task := trace.NewTask(ctx, "restore_test_db")
	defer task.End()

	if !m.Ready() {
		return ErrManagerNotReady
	}

	// check if the template exists and is finalized
	template, found := m.templates.Get(ctx, hash)
	if !found {
		return m.dropDatabaseWithID(ctx, hash, id)
	}

	if template.WaitUntilFinalized(ctx, m.config.TemplateFinalizeTimeout) !=
		templates.TemplateStateFinalized {

		return ErrInvalidTemplateState
	}

	// template is ready, we can returb the testDB to the pool and have it cleaned up
	if err := m.pool.RestoreTestDatabase(ctx, hash, id); err != nil {
		if !(errors.Is(err, pool.ErrInvalidIndex) ||
			errors.Is(err, pool.ErrUnknownHash)) {
			// other error is an internal error
			return err
		}

		// db is not tracked in the pool
		// try to drop it if exists
		return m.dropDatabaseWithID(ctx, hash, id)
	}

	return nil
}

func (m *Manager) ClearTrackedTestDatabases(ctx context.Context, hash string) error {
	if !m.Ready() {
		return ErrManagerNotReady
	}

	removeFunc := func(testDB db.TestDatabase) error {
		return m.dropDatabase(ctx, testDB.Config.Database)
	}

	err := m.pool.RemoveAllWithHash(ctx, hash, removeFunc)
	if errors.Is(err, pool.ErrUnknownHash) {
		return ErrTemplateNotFound
	}

	return err
}

func (m *Manager) ResetAllTracking(ctx context.Context) error {
	if !m.Ready() {
		return ErrManagerNotReady
	}

	// remove all templates to disallow any new test DB creation from existing templates
	m.templates.RemoveAll(ctx)

	removeFunc := func(testDB db.TestDatabase) error {
		return m.dropDatabase(ctx, testDB.Config.Database)
	}
	if err := m.pool.RemoveAll(ctx, removeFunc); err != nil {
		return err
	}

	return nil
}

func (m *Manager) dropDatabaseWithID(ctx context.Context, hash string, id int) error {
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

func (m *Manager) checkDatabaseExists(ctx context.Context, dbName string) (bool, error) {
	var exists bool
	if err := m.db.QueryRowContext(ctx, "SELECT 1 AS exists FROM pg_database WHERE datname = $1", dbName).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}

		return false, err
	}

	return exists, nil
}

func (m *Manager) createDatabase(ctx context.Context, dbName string, owner string, template string) error {

	defer trace.StartRegion(ctx, "create_db").End()

	if _, err := m.db.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s WITH OWNER %s TEMPLATE %s", pq.QuoteIdentifier(dbName), pq.QuoteIdentifier(owner), pq.QuoteIdentifier(template))); err != nil {
		return err
	}

	return nil
}

func (m *Manager) recreateTestDB(ctx context.Context, testDB db.TestDatabase, templateName string) error {
	return m.dropAndCreateDatabase(ctx, testDB.Database.Config.Database, m.config.TestDatabaseOwner, templateName)
}

func (m *Manager) dropDatabase(ctx context.Context, dbName string) error {

	defer trace.StartRegion(ctx, "drop_db").End()

	if _, err := m.db.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", pq.QuoteIdentifier(dbName))); err != nil {
		if strings.Contains(err.Error(), "is being accessed by other users") {
			return ErrTestDBInUse
		}

		return err
	}

	return nil
}

func (m *Manager) dropAndCreateDatabase(ctx context.Context, dbName string, owner string, template string) error {
	if !m.Ready() {
		return ErrManagerNotReady
	}

	if err := m.dropDatabase(ctx, dbName); err != nil {
		return err
	}

	return m.createDatabase(ctx, dbName, owner, template)
}

// createTestDatabaseFromTemplate adds a new test database in the pool (increasing its size) basing on the given template.
// It waits until the template is finalized.
func (m *Manager) createTestDatabaseFromTemplate(ctx context.Context, template *templates.Template) error {
	if template.WaitUntilFinalized(ctx, m.config.TemplateFinalizeTimeout) != templates.TemplateStateFinalized {
		// if the state changed in the meantime, return
		return ErrInvalidTemplateState
	}

	return m.pool.AddTestDatabase(ctx, template.Database, m.recreateTestDB)
}

// Adds new test databases for a template, intended to be run asynchronously from other operations in a separate goroutine, using the manager's WaitGroup to synchronize for shutdown.
func (m *Manager) addInitialTestDatabasesInBackground(template *templates.Template, count int) {

	ctx := m.connectionCtx

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()

		for i := 0; i < count; i++ {
			if err := m.createTestDatabaseFromTemplate(ctx, template); err != nil {
				// TODO anna: error handling
				// fmt.Printf("integresql: failed to initialize DB: %v\n", err)
			}
		}
	}()

}

func (m *Manager) makeTemplateDatabaseName(hash string) string {
	return fmt.Sprintf("%s_%s_%s", m.config.DatabasePrefix, m.config.TemplateDatabasePrefix, hash)
}
