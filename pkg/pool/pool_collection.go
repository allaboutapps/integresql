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

type PoolConfig struct {
	MaxPoolSize      int
	TestDBNamePrefix string
	NumOfWorkers     int  // Number of cleaning workers (each hash pool has enables this number of workers)
	ForceDBReturn    bool // Force returning test DB. If set to false, test databases that are 'dirty' can be recycled (in not actually used).
}

type PoolCollection struct {
	PoolConfig

	pools map[string]*dbHashPool // map[hash]
	mutex sync.RWMutex
}

// forceDBReturn set to false will allow reusing test databases that are marked as 'dirty'.
// Otherwise, test DB has to be returned when no longer needed and there are higher chances of getting ErrPoolFull when requesting a new DB.
func NewPoolCollection(cfg PoolConfig) *PoolCollection {
	return &PoolCollection{
		pools:      make(map[string]*dbHashPool),
		PoolConfig: cfg,
	}
}

// RecreateDBFunc callback executed when a pool is extended or the DB cleaned up by a worker.
type RecreateDBFunc func(ctx context.Context, testDB db.TestDatabase, templateName string) error

func makeActualRecreateTestDBFunc(templateName string, userRecreateFunc RecreateDBFunc) recreateTestDBFunc {
	return func(ctx context.Context, testDBWrapper *existingDB) error {
		testDBWrapper.createdAt = time.Now()
		return userRecreateFunc(ctx, testDBWrapper.TestDatabase, templateName)
	}
}

type recreateTestDBFunc func(context.Context, *existingDB) error

// InitHashPool creates a new pool with a given template hash and starts the cleanup workers.
func (p *PoolCollection) InitHashPool(ctx context.Context, templateDB db.Database, initDBFunc RecreateDBFunc) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	_ = p.initHashPool(ctx, templateDB, initDBFunc)
}

func (p *PoolCollection) initHashPool(ctx context.Context, templateDB db.Database, initDBFunc RecreateDBFunc) *dbHashPool {
	// create a new dbHashPool
	pool := newDBHashPool(p.PoolConfig, templateDB, initDBFunc)
	// and start the cleaning worker
	pool.enableWorker(p.NumOfWorkers)

	// pool is ready
	p.pools[pool.templateDB.TemplateHash] = pool

	return pool
}

// Stop is used to stop all background workers
func (p *PoolCollection) Stop() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	for _, pool := range p.pools {
		close(pool.waitingForCleaning)
		pool.wg.Wait()
	}

}

// GetTestDatabase picks up a ready to use test DB. It waits the given timeout until a DB is available.
// If there is no DB ready and time elapses, ErrTimeout is returned.
// Otherwise, the obtained test DB is marked as 'dirty' and can be reused only if returned to the pool.
func (p *PoolCollection) GetTestDatabase(ctx context.Context, hash string, timeout time.Duration) (db db.TestDatabase, err error) {

	// !
	// PoolCollection locked
	reg := trace.StartRegion(ctx, "wait_for_lock_main_pool")
	p.mutex.RLock()
	reg.End()
	pool := p.pools[hash]

	if pool == nil {
		// no such pool
		p.mutex.RUnlock()
		err = ErrUnknownHash
		return
	}

	p.mutex.RUnlock()
	// PoolCollection unlocked
	// !

	var index int
	select {
	case <-time.After(timeout):
		err = ErrTimeout
		return
	case index = <-pool.ready:
	}

	// !
	// dbHashPool locked
	reg = trace.StartRegion(ctx, "wait_for_lock_hash_pool")
	pool.Lock()
	defer pool.Unlock()
	reg.End()

	// sanity check, should never happen
	if index < 0 || index >= len(pool.dbs) {
		err = ErrInvalidIndex
		return
	}

	testDB := pool.dbs[index]
	// sanity check, should never happen - we got this index from 'ready' channel
	if testDB.state != dbStateReady {
		err = ErrInvalidState
		return
	}

	testDB.state = dbStateDirty
	pool.dbs[index] = testDB

	select {
	case pool.dirty <- index:
		// sent to dirty without blocking
	default:
		// channel is full
	}

	return testDB.TestDatabase, nil
	// dbHashPool unlocked
	// !
}

// AddTestDatabase adds a new test DB to the pool and creates it according to the template.
// The new test DB is marked as 'Ready' and can be picked up with GetTestDatabase.
// If the pool size has already reached MAX, ErrPoolFull is returned, unless ForceDBReturn flag is set to false.
// Then databases that were given away would get reset (if no DB connection is currently open) and marked as 'Ready'.
func (p *PoolCollection) AddTestDatabase(ctx context.Context, templateDB db.Database, initFunc RecreateDBFunc) error {
	hash := templateDB.TemplateHash

	// !
	// PoolCollection locked
	reg := trace.StartRegion(ctx, "wait_for_lock_main_pool")
	p.mutex.Lock()
	reg.End()
	pool := p.pools[hash]

	if pool == nil {
		pool = p.initHashPool(ctx, templateDB, initFunc)
	}

	forceReturn := p.ForceDBReturn
	p.mutex.Unlock()
	// PoolCollection unlocked
	// !

	newTestDB, err := pool.extend(ctx, dbStateReady, p.PoolConfig.TestDBNamePrefix)
	if err != nil {
		if errors.Is(err, ErrPoolFull) && !forceReturn {
			// we can try to reset test databases that are 'dirty'
			_, err := pool.resetNotReturned(ctx, p.TestDBNamePrefix, false /* shouldKeepDirty */)
			return err
		}

		return err
	}

	// and add its index to 'ready'
	pool.ready <- newTestDB.ID

	return nil
}

// AddTestDatabase adds a new test DB to the pool, creates it according to the template, and returns it right away to the caller.
// The new test DB is marked as 'IsUse' and won't be picked up with GetTestDatabase, until it's returned to the pool.
func (p *PoolCollection) ExtendPool(ctx context.Context, templateDB db.Database) (db.TestDatabase, error) {
	hash := templateDB.TemplateHash

	// !
	// PoolCollection locked
	reg := trace.StartRegion(ctx, "wait_for_lock_main_pool")
	p.mutex.Lock()
	reg.End()
	pool := p.pools[hash]

	if pool == nil {
		// meant to be only for already initialized pools
		p.mutex.Unlock()
		return db.TestDatabase{}, ErrUnknownHash
	}

	forceReturn := p.ForceDBReturn
	p.mutex.Unlock()
	// PoolCollection unlocked
	// !

	// because we return it right away, we treat it as 'dirty'
	testDB, err := pool.extend(ctx, dbStateDirty, p.PoolConfig.TestDBNamePrefix)
	if err != nil {
		if errors.Is(err, ErrPoolFull) && !forceReturn {
			// we can try to reset test databases that are 'dirty'
			return pool.resetNotReturned(ctx, p.TestDBNamePrefix, true /* shouldKeepDirty */)
		}

		return db.TestDatabase{}, err
	}

	select {
	case pool.dirty <- testDB.ID:
		// sent to dirty without blocking
	default:
		// channel is full
	}

	return testDB, nil
}

// RestoreTestDatabase recreates the given test DB and returns it back to the pool.
// To have it recreated, it is added to 'waitingForCleaning' channel.
// If the test DB is in a different state than 'dirty', ErrInvalidState is returned.
func (p *PoolCollection) RestoreTestDatabase(ctx context.Context, hash string, id int) error {

	// !
	// PoolCollection locked
	reg := trace.StartRegion(ctx, "wait_for_lock_main_pool")
	p.mutex.Lock()
	reg.End()
	pool := p.pools[hash]

	if pool == nil {
		// no such pool
		p.mutex.Unlock()
		return ErrUnknownHash
	}

	// !
	// dbHashPool locked
	reg = trace.StartRegion(ctx, "wait_for_lock_hash_pool")
	pool.Lock()
	defer pool.Unlock()
	reg.End()

	p.mutex.Unlock()
	// PoolCollection unlocked
	// !

	if id < 0 || id >= len(pool.dbs) {
		return ErrInvalidIndex
	}

	// check if db is in the correct state
	testDB := pool.dbs[id]
	if testDB.state == dbStateReady {
		return nil
	}

	if testDB.state != dbStateDirty {
		return ErrInvalidState
	}

	testDB.state = dbStateWaitingForCleaning
	pool.dbs[id] = testDB

	// add it to waitingForCleaning channel, to have it cleaned up by the worker
	pool.waitingForCleaning <- id

	return nil
	// dbHashPool unlocked
	// !
}

// ReturnTestDatabase returns the given test DB directly to the pool, without cleaning (recreating it).
// If the test DB is in a different state than 'dirty', ErrInvalidState is returned.
func (p *PoolCollection) ReturnTestDatabase(ctx context.Context, hash string, id int) error {
	// !
	// PoolCollection locked
	reg := trace.StartRegion(ctx, "wait_for_lock_main_pool")
	p.mutex.Lock()
	reg.End()
	pool := p.pools[hash]

	if pool == nil {
		// no such pool
		p.mutex.Unlock()
		return ErrUnknownHash
	}
	p.mutex.Unlock()

	pool.Lock()
	defer pool.Unlock()

	if id < 0 || id >= len(pool.dbs) {
		return ErrInvalidIndex
	}

	// check if db is in the correct state
	testDB := pool.dbs[id]
	if testDB.state == dbStateReady {
		return nil
	}

	// if not in use, it will be cleaned up by a worker
	if testDB.state != dbStateDirty {
		return ErrInvalidState
	}

	testDB.state = dbStateReady
	pool.dbs[id] = testDB

	pool.ready <- id

	return nil
}

// RemoveAllWithHash removes a pool with a given template hash.
// All background workers belonging to this pool are stopped.
func (p *PoolCollection) RemoveAllWithHash(ctx context.Context, hash string, removeFunc func(db.TestDatabase) error) error {

	// !
	// PoolCollection locked
	p.mutex.Lock()
	defer p.mutex.Unlock()

	pool := p.pools[hash]

	if pool == nil {
		// no such pool
		return ErrUnknownHash
	}

	if err := pool.removeAll(removeFunc); err != nil {
		return err
	}

	// all DBs have been removed, now remove the pool itself
	delete(p.pools, hash)

	return nil
	// PoolCollection unlocked
	// !
}

// RemoveAll removes all tracked pools.
func (p *PoolCollection) RemoveAll(ctx context.Context, removeFunc func(db.TestDatabase) error) error {
	// !
	// PoolCollection locked
	p.mutex.Lock()
	defer p.mutex.Unlock()

	for hash, pool := range p.pools {
		if err := pool.removeAll(removeFunc); err != nil {
			return err
		}

		delete(p.pools, hash)
	}

	return nil
	// PoolCollection unlocked
	// !
}

// MakeDBName makes a test DB name with the configured prefix, template hash and ID of the DB.
func (p *PoolCollection) MakeDBName(hash string, id int) string {
	p.mutex.RLock()
	p.mutex.RUnlock()

	return makeDBName(p.PoolConfig.TestDBNamePrefix, hash, id)
}

func makeDBName(testDBPrefix string, hash string, id int) string {
	// db name has an ID in suffix
	return fmt.Sprintf("%s%s_%03d", testDBPrefix, hash, id)
}
