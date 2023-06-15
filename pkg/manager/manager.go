package manager

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime/trace"
	"sort"
	"sync"
	"time"

	"github.com/lib/pq"
)

var (
	ErrManagerNotReady            = errors.New("manager not ready")
	ErrTemplateAlreadyInitialized = errors.New("template is already initialized")
	ErrTemplateNotFound           = errors.New("template not found")
	ErrTestNotFound               = errors.New("test database not found")
)

type Manager struct {
	config        ManagerConfig
	db            *sql.DB
	templates     map[string]*TemplateDatabase
	templateMutex sync.RWMutex
	wg            sync.WaitGroup
}

func New(config ManagerConfig) *Manager {
	m := &Manager{
		config:    config,
		db:        nil,
		templates: map[string]*TemplateDatabase{},
		wg:        sync.WaitGroup{},
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

	return m
}

func DefaultFromEnv() *Manager {
	return New(DefaultManagerConfigFromEnv())
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

	c := make(chan struct{})
	go func() {
		defer close(c)
		m.wg.Wait()
	}()

	select {
	case <-c:
	case <-ctx.Done():
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

func (m *Manager) InitializeTemplateDatabase(ctx context.Context, hash string) (*TemplateDatabase, error) {
	ctx, task := trace.NewTask(ctx, "initialize_template_db")
	defer task.End()

	if !m.Ready() {
		return nil, ErrManagerNotReady
	}

	reg := trace.StartRegion(ctx, "get_template_lock")
	m.templateMutex.Lock()
	defer m.templateMutex.Unlock()
	reg.End()

	_, ok := m.templates[hash]

	if ok {
		// fmt.Println("initialized!", ok)
		return nil, ErrTemplateAlreadyInitialized
	}

	// fmt.Println("initializing...", ok)

	dbName := fmt.Sprintf("%s_%s_%s", m.config.DatabasePrefix, m.config.TemplateDatabasePrefix, hash)
	template := &TemplateDatabase{
		Database: Database{
			TemplateHash: hash,
			Config: DatabaseConfig{
				Host:     m.config.ManagerDatabaseConfig.Host,
				Port:     m.config.ManagerDatabaseConfig.Port,
				Username: m.config.ManagerDatabaseConfig.Username,
				Password: m.config.ManagerDatabaseConfig.Password,
				Database: dbName,
			},
			state: databaseStateInit,
			c:     make(chan struct{}),
		},
		nextTestID:    0,
		testDatabases: make([]*TestDatabase, 0),
	}

	m.templates[hash] = template

	reg = trace.StartRegion(ctx, "drop_and_create_db")
	if err := m.dropAndCreateDatabase(ctx, dbName, m.config.ManagerDatabaseConfig.Username, m.config.TemplateDatabaseTemplate); err != nil {
		delete(m.templates, hash)

		return nil, err
	}
	reg.End()

	return template, nil
}

func (m *Manager) DiscardTemplateDatabase(ctx context.Context, hash string) error {

	ctx, task := trace.NewTask(ctx, "discard_template_db")
	defer task.End()

	if !m.Ready() {
		return ErrManagerNotReady
	}

	reg := trace.StartRegion(ctx, "get_template_lock")
	m.templateMutex.Lock()
	defer m.templateMutex.Unlock()
	reg.End()

	template, ok := m.templates[hash]

	if !ok {
		dbName := fmt.Sprintf("%s_%s_%s", m.config.DatabasePrefix, m.config.TemplateDatabasePrefix, hash)
		exists, err := m.checkDatabaseExists(ctx, dbName)
		if err != nil {
			return err
		}

		if !exists {
			return ErrTemplateNotFound
		}
	}

	// discard any still waiting dbs.
	template.FlagAsDiscarded(ctx)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := template.WaitUntilReady(ctx); err != nil {
		cancel()
	}
	cancel()

	delete(m.templates, hash)

	return nil
}

func (m *Manager) FinalizeTemplateDatabase(ctx context.Context, hash string) (*TemplateDatabase, error) {
	ctx, task := trace.NewTask(ctx, "finalize_template_db")
	defer task.End()

	if !m.Ready() {
		return nil, ErrManagerNotReady
	}

	reg := trace.StartRegion(ctx, "get_template_lock")
	m.templateMutex.Lock()
	defer m.templateMutex.Unlock()
	reg.End()

	template, ok := m.templates[hash]

	// We don't allow finalizing NEVER initialized database by integresql!
	if !ok {
		return nil, ErrTemplateNotFound
	}

	state := template.State(ctx)

	// early bailout if we are already ready (multiple calls)
	if state == databaseStateReady {
		return template, nil
	}

	// Disallow transition from discarded to ready
	if state == databaseStateDiscarded {
		return nil, ErrDatabaseDiscarded
	}

	template.FlagAsReady(ctx)

	m.wg.Add(1)
	go m.addTestDatabasesInBackground(template, m.config.TestDatabaseInitialPoolSize)

	return template, nil
}

func (m *Manager) GetTestDatabase(ctx context.Context, hash string) (*TestDatabase, error) {
	ctx, task := trace.NewTask(ctx, "get_test_db")
	defer task.End()

	if !m.Ready() {
		return nil, ErrManagerNotReady
	}

	reg := trace.StartRegion(ctx, "get_template_lock")
	m.templateMutex.RLock()
	reg.End()
	template, ok := m.templates[hash]
	m.templateMutex.RUnlock()

	if !ok {
		return nil, ErrTemplateNotFound
	}

	if err := template.WaitUntilReady(ctx); err != nil {
		return nil, err
	}

	template.Lock()
	defer template.Unlock()

	var testDB *TestDatabase
	for _, db := range template.testDatabases {
		if db.ReadyForTest(ctx) {
			testDB = db
			break
		}
	}

	if testDB == nil {
		var err error
		testDB, err = m.createNextTestDatabase(ctx, template)
		if err != nil {
			return nil, err
		}
	}

	testDB.FlagAsDirty(ctx)

	m.wg.Add(1)
	go m.addTestDatabasesInBackground(template, 1)

	return testDB, nil
}

func (m *Manager) ReturnTestDatabase(ctx context.Context, hash string, id int) error {
	if !m.Ready() {
		return ErrManagerNotReady
	}

	m.templateMutex.RLock()
	template, ok := m.templates[hash]
	m.templateMutex.RUnlock()

	if !ok {
		return ErrTemplateNotFound
	}

	if err := template.WaitUntilReady(ctx); err != nil {
		return err
	}

	template.Lock()
	defer template.Unlock()

	found := false
	for _, db := range template.testDatabases {
		if db.ID == id {
			found = true
			db.FlagAsClean(ctx)
			break
		}
	}

	if !found {
		dbName := fmt.Sprintf("%s_%s_%s_%03d", m.config.DatabasePrefix, m.config.TestDatabasePrefix, hash, id)
		exists, err := m.checkDatabaseExists(ctx, dbName)
		if err != nil {
			return err
		}

		if !exists {
			return ErrTestNotFound
		}

		db := &TestDatabase{
			Database: Database{
				TemplateHash: hash,
				Config: DatabaseConfig{
					Host:     m.config.ManagerDatabaseConfig.Host,
					Port:     m.config.ManagerDatabaseConfig.Port,
					Username: m.config.TestDatabaseOwner,
					Password: m.config.TestDatabaseOwnerPassword,
					Database: dbName,
				},
				state: databaseStateReady,
				c:     make(chan struct{}),
			},
			ID:    id,
			dirty: false,
		}

		template.testDatabases = append(template.testDatabases, db)
		sort.Sort(ByID(template.testDatabases))
	}

	return nil
}

func (m *Manager) ClearTrackedTestDatabases(hash string) error {
	if !m.Ready() {
		return ErrManagerNotReady
	}

	m.templateMutex.RLock()
	template, ok := m.templates[hash]
	m.templateMutex.RUnlock()

	if !ok {
		return ErrTemplateNotFound
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := template.WaitUntilReady(ctx); err != nil {
		cancel()
		return err
	}
	cancel()

	template.Lock()
	defer template.Unlock()

	for i := range template.testDatabases {
		template.testDatabases[i] = nil
	}

	template.testDatabases = make([]*TestDatabase, 0)
	template.nextTestID = 0

	return nil
}

func (m *Manager) ResetAllTracking() error {
	if !m.Ready() {
		return ErrManagerNotReady
	}

	m.templateMutex.Lock()
	defer m.templateMutex.Unlock()

	for hash := range m.templates {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := m.templates[hash].WaitUntilReady(ctx); err != nil {
			cancel()
			continue
		}
		cancel()

		m.templates[hash].Lock()
		for i := range m.templates[hash].testDatabases {
			m.templates[hash].testDatabases[i] = nil
		}
		m.templates[hash].Unlock()

		delete(m.templates, hash)
		// m.templates[hash] = nil
	}

	m.templates = map[string]*TemplateDatabase{}

	return nil
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

func (m *Manager) dropDatabase(ctx context.Context, dbName string) error {

	defer trace.StartRegion(ctx, "drop_db").End()

	if _, err := m.db.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", pq.QuoteIdentifier(dbName))); err != nil {
		return err
	}

	return nil
}

func (m *Manager) dropAndCreateDatabase(ctx context.Context, dbName string, owner string, template string) error {
	if err := m.dropDatabase(ctx, dbName); err != nil {
		return err
	}

	return m.createDatabase(ctx, dbName, owner, template)
}

// Creates a new test database for the template and increments the next ID.
// ! ATTENTION: this function assumes `template` has already been LOCKED by its caller and will NOT synchronize access again !
// The newly created database object is returned as well as added to the template's DB list automatically.
func (m *Manager) createNextTestDatabase(ctx context.Context, template *TemplateDatabase) (*TestDatabase, error) {
	dbName := fmt.Sprintf("%s_%s_%s_%03d", m.config.DatabasePrefix, m.config.TestDatabasePrefix, template.TemplateHash, template.nextTestID)

	if err := m.dropAndCreateDatabase(ctx, dbName, m.config.TestDatabaseOwner, template.Config.Database); err != nil {
		return nil, err
	}

	testDB := &TestDatabase{
		Database: Database{
			TemplateHash: template.TemplateHash,
			Config: DatabaseConfig{
				Host:     m.config.ManagerDatabaseConfig.Host,
				Port:     m.config.ManagerDatabaseConfig.Port,
				Username: m.config.TestDatabaseOwner,
				Password: m.config.TestDatabaseOwnerPassword,
				Database: dbName,
			},
			state: databaseStateReady,
			c:     make(chan struct{}),
		},
		ID:    template.nextTestID,
		dirty: false,
	}

	template.testDatabases = append(template.testDatabases, testDB)
	template.nextTestID++

	if template.nextTestID > m.config.TestDatabaseMaxPoolSize {
		i := 0
		for idx, db := range template.testDatabases {
			if db.Dirty(ctx) {
				i = idx
				break
			}
		}

		if err := m.dropDatabase(ctx, template.testDatabases[i].Config.Database); err != nil {
			return nil, err
		}

		// Delete while preserving order, avoiding memory leaks due to points in accordance to: https://github.com/golang/go/wiki/SliceTricks
		if i < len(template.testDatabases)-1 {
			copy(template.testDatabases[i:], template.testDatabases[i+1:])
		}
		template.testDatabases[len(template.testDatabases)-1] = nil
		template.testDatabases = template.testDatabases[:len(template.testDatabases)-1]
	}

	return testDB, nil
}

// Adds new test databases for a template, intended to be run asynchronously from other operations in a separate goroutine, using the manager's WaitGroup to synchronize for shutdown.
// This function will lock `template` until all requested test DBs have been created and signal the WaitGroup about completion afterwards.
func (m *Manager) addTestDatabasesInBackground(template *TemplateDatabase, count int) {
	defer m.wg.Done()

	template.Lock()
	defer template.Unlock()

	ctx := context.Background()

	for i := 0; i < count; i++ {
		// TODO log error somewhere instead of silently swallowing it?
		_, _ = m.createNextTestDatabase(ctx, template)
	}
}
