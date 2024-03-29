package pool

import (
	"context"
	"errors"
	"fmt"
	"runtime/trace"
	"sync"
	"time"

	"github.com/allaboutapps/integresql/pkg/db"
)

var ErrUnknownHash = errors.New("no database pool exists for this hash")

// we explicitly want to access this struct via pool.PoolConfig, thus we disable revive for the next line
type PoolConfig struct { //nolint:revive
	InitialPoolSize                   int           // Initial number of ready DBs prepared in background
	MaxPoolSize                       int           // Maximal pool size that won't be exceeded
	TestDBNamePrefix                  string        // Test-Database prefix: DatabasePrefix_TestDBNamePrefix_HASH_ID
	MaxParallelTasks                  int           // Maximal number of pool tasks running in parallel. Must be a number greater or equal 1.
	TestDatabaseRetryRecreateSleepMin time.Duration // Minimal time to wait after a test db recreate has failed (e.g. as client is still connected). Subsequent retries multiply this values until...
	TestDatabaseRetryRecreateSleepMax time.Duration // ... the maximum possible sleep time between retries (e.g. 3 seconds) is reached.
	TestDatabaseMinimalLifetime       time.Duration // After a testdatabase transitions from ready to dirty, always block auto-recreation for this duration (except manual recreate).

	disableWorkerAutostart bool // test only private flag for starting without background worker task system
}

// we explicitly want to access this struct via pool.PoolCollection, thus we disable revive for the next line
type PoolCollection struct { //nolint:revive
	PoolConfig

	pools map[string]*HashPool // map[hash]
	mutex sync.RWMutex
}

// enableDBRecreate set to false will allow reusing test databases that are marked as 'dirty'.
// Otherwise, test DB has to be returned when no longer needed and there are higher chances of getting ErrPoolFull when requesting a new DB.
func NewPoolCollection(cfg PoolConfig) *PoolCollection {
	return &PoolCollection{
		pools:      make(map[string]*HashPool),
		PoolConfig: cfg,
	}
}

// RecreateDBFunc callback executed when a pool is extended or the DB cleaned up by a worker.
type RecreateDBFunc func(ctx context.Context, testDB db.TestDatabase, templateName string) error

// RemoveDBFunc callback executed to remove a database
type RemoveDBFunc func(ctx context.Context, testDB db.TestDatabase) error

func makeActualRecreateTestDBFunc(templateName string, userRecreateFunc RecreateDBFunc) recreateTestDBFunc {
	return func(ctx context.Context, testDBWrapper *existingDB) error {
		return userRecreateFunc(ctx, testDBWrapper.TestDatabase, templateName)
	}
}

type recreateTestDBFunc func(context.Context, *existingDB) error

// InitHashPool creates a new pool with a given template hash and starts the cleanup workers.
func (p *PoolCollection) InitHashPool(_ context.Context, templateDB db.Database, initDBFunc RecreateDBFunc) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	cfg := p.PoolConfig

	// Create a new HashPool
	pool := NewHashPool(cfg, templateDB, initDBFunc)

	if !cfg.disableWorkerAutostart {
		pool.Start()
	}

	// pool is ready
	p.pools[pool.templateDB.TemplateHash] = pool
}

// Start is used to start all background workers
func (p *PoolCollection) Start() {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	for _, pool := range p.pools {
		pool.Start()
	}
}

// Stop is used to stop all background workers
func (p *PoolCollection) Stop() {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	for _, pool := range p.pools {
		pool.Stop()
	}
}

// GetTestDatabase picks up a ready to use test DB. It waits the given timeout until a DB is available.
// If there is no DB ready and time elapses, ErrTimeout is returned.
// Otherwise, the obtained test DB is marked as 'dirty' and can be reused only if returned to the pool.
func (p *PoolCollection) GetTestDatabase(ctx context.Context, hash string, timeout time.Duration) (db db.TestDatabase, err error) {

	pool, err := p.getPool(ctx, hash)
	if err != nil {
		return db, err
	}

	return pool.GetTestDatabase(ctx, timeout)
}

// ReturnTestDatabase returns the given test DB directly to the pool, without cleaning (recreating it).
func (p *PoolCollection) ReturnTestDatabase(ctx context.Context, hash string, id int) error {
	pool, err := p.getPool(ctx, hash)
	if err != nil {
		return err
	}

	return pool.ReturnTestDatabase(ctx, id)
}

// RecreateTestDatabase recreates the test DB according to the template and returns it back to the pool.
func (p *PoolCollection) RecreateTestDatabase(ctx context.Context, hash string, id int) error {
	pool, err := p.getPool(ctx, hash)
	if err != nil {
		return err
	}

	return pool.RecreateTestDatabase(ctx, id)
}

// RemoveAllWithHash removes a pool with a given template hash.
// All background workers belonging to this pool are stopped.
func (p *PoolCollection) RemoveAllWithHash(ctx context.Context, hash string, removeFunc RemoveDBFunc) error {
	pool, collUnlock, err := p.getPoolLockCollection(ctx, hash)
	defer collUnlock()

	if err != nil {
		return err
	}

	if err := pool.RemoveAll(ctx, removeFunc); err != nil {
		return err
	}

	// all DBs have been removed, now remove the pool itself
	delete(p.pools, hash)

	return nil
}

// RemoveAll removes all tracked pools.
func (p *PoolCollection) RemoveAll(ctx context.Context, removeFunc RemoveDBFunc) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	for hash, pool := range p.pools {
		if err := pool.RemoveAll(ctx, removeFunc); err != nil {
			return err
		}

		delete(p.pools, hash)
	}

	return nil
}

// MakeDBName makes a test DB name with the configured prefix, template hash and ID of the DB.
func (p *PoolCollection) MakeDBName(hash string, id int) string {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	return makeDBName(p.PoolConfig.TestDBNamePrefix, hash, id)
}

func makeDBName(testDBPrefix string, hash string, id int) string {
	// db name has an ID in suffix
	return fmt.Sprintf("%s%s_%03d", testDBPrefix, hash, id)
}

func (p *PoolCollection) getPool(ctx context.Context, hash string) (pool *HashPool, err error) {
	reg := trace.StartRegion(ctx, "wait_for_rlock_main_pool")
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	reg.End()

	pool, ok := p.pools[hash]
	if !ok {
		// no such pool
		return nil, ErrUnknownHash
	}

	return pool, nil
}

func (p *PoolCollection) getPoolLockCollection(ctx context.Context, hash string) (pool *HashPool, unlock func(), err error) {
	reg := trace.StartRegion(ctx, "wait_for_lock_main_pool")
	p.mutex.Lock()
	unlock = func() { p.mutex.Unlock() }
	reg.End()

	pool, ok := p.pools[hash]
	if !ok {
		// no such pool
		err = ErrUnknownHash
	}

	return pool, unlock, err
}

// extend is only used for internal testing!
// it adds a new test DB to the pool and creates it according to the template.
// The new test DB is marked as 'Ready' and can be picked up with GetTestDatabase.
// If the pool size has already reached MAX, ErrPoolFull is returned.
func (p *PoolCollection) extend(ctx context.Context, templateDB db.Database) error {
	hash := templateDB.TemplateHash

	pool, err := p.getPool(ctx, hash)
	if err != nil {
		return err
	}

	return pool.extend(ctx)
}
