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

var (
	ErrUnknownHash  = errors.New("no database pool exists for this hash")
	ErrPoolFull     = errors.New("database pool is full")
	ErrInvalidState = errors.New("database state is not valid for this operation")
	ErrInvalidIndex = errors.New("invalid database index (id)")
	ErrTimeout      = errors.New("timeout when waiting for ready db")
)

type dbState int // Indicates a current DB state.

const (
	dbStateReady dbState = iota // Initialized according to a template and ready to be picked up.
	dbStateInUse                // Currently in use, can't be reused.
	dbStateDirty                // Returned to the pool, waiting for the cleaning.
)

const stopWorkerMessage int = -1

type DBPool struct {
	pools map[string]*dbHashPool // map[hash]
	mutex sync.RWMutex

	maxPoolSize  int
	dbNamePrefix string
	numOfWorkers int // Number of cleaning workers (each hash pool has enables this number of workers)
}

func NewDBPool(maxPoolSize int, testDBNamePrefix string, numberOfWorkers int) *DBPool {
	return &DBPool{
		pools: make(map[string]*dbHashPool),

		maxPoolSize:  maxPoolSize,
		dbNamePrefix: testDBNamePrefix,
		numOfWorkers: numberOfWorkers,
	}
}

// RecreateDBFunc callback executed when a pool is extended or the DB cleaned up by a worker.
type RecreateDBFunc func(ctx context.Context, testDB db.TestDatabase, templateName string) error

type existingDB struct {
	state dbState
	db.TestDatabase
}

// dbHashPool holds a test DB pool for a certain hash. Each dbHashPool is running cleanup workers in background.
type dbHashPool struct {
	dbs   []existingDB
	ready chan int // ID of initalized DBs according to a template, ready to pick them up
	dirty chan int // ID of returned DBs, need to be recreated to reuse them

	recreateDB RecreateDBFunc
	templateDB db.Database
	sync.RWMutex
	wg           sync.WaitGroup
	numOfWorkers int
}

// InitHashPool creates a new pool with a given template hash and starts the cleanup workers.
func (p *DBPool) InitHashPool(ctx context.Context, templateDB db.Database, initDBFunc RecreateDBFunc) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	_ = p.initHashPool(ctx, templateDB, initDBFunc)
}

func (p *DBPool) initHashPool(ctx context.Context, templateDB db.Database, initDBFunc RecreateDBFunc) *dbHashPool {
	// create a new dbHashPool
	pool := newDBHashPool(p.maxPoolSize, initDBFunc, templateDB, p.numOfWorkers)
	// and start the cleaning worker
	pool.enableWorker(p.numOfWorkers)

	// pool is ready
	p.pools[pool.templateDB.TemplateHash] = pool

	return pool
}

// Stop is used to stop all background workers
func (p *DBPool) Stop() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	for _, pool := range p.pools {
		close(pool.dirty)
		pool.wg.Wait()
	}

}

// GetTestDatabase picks up a ready to use test DB. It waits the given timeout until a DB is available.
// If there is no DB ready and time elapses, ErrTimeout is returned.
// Otherwise, the obtained test DB is marked as 'InUse' and can be reused only if returned to the pool.
func (p *DBPool) GetTestDatabase(ctx context.Context, hash string, timeout time.Duration) (db db.TestDatabase, err error) {

	// !
	// DBPool locked
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
	// DBPool unlocked
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

	givenTestDB := pool.dbs[index]
	// sanity check, should never happen - we got this index from 'ready' channel
	if givenTestDB.state != dbStateReady {
		err = ErrInvalidState
		return
	}

	givenTestDB.state = dbStateInUse
	pool.dbs[index] = givenTestDB

	return givenTestDB.TestDatabase, nil
	// dbHashPool unlocked
	// !
}

// AddTestDatabase adds a new test DB to the pool and creates it according to the template.
// The new test DB is marked as 'Ready' and can be picked up with GetTestDatabase.
// If the pool size has already reached MAX, ErrPoolFull is returned.
func (p *DBPool) AddTestDatabase(ctx context.Context, templateDB db.Database, initFunc RecreateDBFunc) error {
	hash := templateDB.TemplateHash

	// !
	// DBPool locked
	reg := trace.StartRegion(ctx, "wait_for_lock_main_pool")
	p.mutex.Lock()
	reg.End()
	pool := p.pools[hash]

	if pool == nil {
		pool = p.initHashPool(ctx, templateDB, initFunc)
	}

	p.mutex.Unlock()
	// DBPool unlocked
	// !

	newTestDB, err := pool.extend(ctx, dbStateReady, p.dbNamePrefix)
	if err != nil {
		return err
	}

	// and add its index to 'ready'
	pool.ready <- newTestDB.ID

	return nil
}

// AddTestDatabase adds a new test DB to the pool, creates it according to the template, and returns it right away to the caller.
// The new test DB is marked as 'IsUse' and won't be picked up with GetTestDatabase, until it's returned to the pool.
// If the pool size has already reached MAX, ErrPoolFull is returned.
func (p *DBPool) ExtendPool(ctx context.Context, templateDB db.Database) (db.TestDatabase, error) {
	hash := templateDB.TemplateHash

	// !
	// DBPool locked
	reg := trace.StartRegion(ctx, "wait_for_lock_main_pool")
	p.mutex.Lock()
	reg.End()
	pool := p.pools[hash]

	if pool == nil {
		// meant to be only for already initialized pools
		p.mutex.Unlock()
		return db.TestDatabase{}, ErrUnknownHash
	}

	p.mutex.Unlock()
	// DBPool unlocked
	// !

	// because we return it right away, we treat it as 'inUse'
	newTestDB, err := pool.extend(ctx, dbStateInUse, p.dbNamePrefix)
	if err != nil {
		return db.TestDatabase{}, err
	}

	return newTestDB, nil
}

// ReturnTestDatabase is used to return a DB that is currently 'InUse' to the pool.
// After successful return, the test DB is cleaned up in the background by a worker.
// If the test DB is in a different state than 'InUse', ErrInvalidState is returned.
func (p *DBPool) ReturnTestDatabase(ctx context.Context, hash string, id int) error {

	// !
	// DBPool locked
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
	// DBPool unlocked
	// !

	if id >= len(pool.dbs) {
		return ErrInvalidIndex
	}

	// check if db is in the correct state
	testDB := pool.dbs[id]
	if testDB.state != dbStateInUse {
		return ErrInvalidState
	}

	testDB.state = dbStateDirty
	pool.dbs[id] = testDB

	// add it to dirty channel, to have it cleaned up by the worker
	pool.dirty <- id

	return nil
	// dbHashPool unlocked
	// !
}

// RemoveAllWithHash removes a pool with a given template hash.
// All background workers belonging to this pool are stopped.
func (p *DBPool) RemoveAllWithHash(ctx context.Context, hash string, removeFunc func(db.TestDatabase) error) error {

	// !
	// DBPool locked
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
	// DBPool unlocked
	// !
}

// RemoveAll removes all tracked pools.
func (p *DBPool) RemoveAll(ctx context.Context, removeFunc func(db.TestDatabase) error) error {
	// !
	// DBPool locked
	p.mutex.Lock()
	defer p.mutex.Unlock()

	for hash, pool := range p.pools {
		if err := pool.removeAll(removeFunc); err != nil {
			return err
		}

		delete(p.pools, hash)
	}

	return nil
	// DBPool unlocked
	// !
}

func newDBHashPool(maxPoolSize int, recreateDB RecreateDBFunc, templateDB db.Database, numberOfWorkers int) *dbHashPool {
	return &dbHashPool{
		dbs:          make([]existingDB, 0, maxPoolSize),
		ready:        make(chan int, maxPoolSize),
		dirty:        make(chan int, maxPoolSize),
		recreateDB:   recreateDB,
		templateDB:   templateDB,
		numOfWorkers: numberOfWorkers,
	}
}

func (pool *dbHashPool) enableWorker(numberOfWorkers int) {
	for i := 0; i < numberOfWorkers; i++ {
		pool.wg.Add(1)
		go func() {
			defer pool.wg.Done()
			pool.workerCleanUpDirtyDB()
		}()
	}
}

// workerCleanUpDirtyDB reads 'dirty' channel and cleans up a test DB with the received index.
// When the DB is recreated according to a template, its index goes to the 'ready' channel.
func (pool *dbHashPool) workerCleanUpDirtyDB() {

	templateName := pool.templateDB.Config.Database

	for dirtyID := range pool.dirty {
		if dirtyID == stopWorkerMessage {
			break
		}

		ctx, task := trace.NewTask(context.Background(), "worker_cleanup_task")

		regLock := trace.StartRegion(ctx, "worker_wait_for_rlock_hash_pool")
		pool.RLock()
		regLock.End()

		if dirtyID < 0 || dirtyID >= len(pool.dbs) {
			// sanity check, should never happen
			pool.RUnlock()
			task.End()
			continue
		}
		testDB := pool.dbs[dirtyID]
		pool.RUnlock()

		if testDB.state != dbStateDirty {
			task.End()
			continue
		}

		reg := trace.StartRegion(ctx, "worker_cleanup")
		if err := pool.recreateDB(ctx, testDB.TestDatabase, templateName); err != nil {
			// TODO anna: error handling
			fmt.Printf("integresql: failed to clean up DB: %v\n", err)

			task.End()
			continue
		}

		regLock = trace.StartRegion(ctx, "worker_wait_for_lock_hash_pool")
		pool.Lock()
		regLock.End()

		testDB.state = dbStateReady
		pool.dbs[dirtyID] = testDB

		pool.Unlock()

		pool.ready <- testDB.ID

		reg.End()
		task.End()
	}
}

func (pool *dbHashPool) extend(ctx context.Context, state dbState, testDBPrefix string) (db.TestDatabase, error) {
	// !
	// dbHashPool locked
	reg := trace.StartRegion(ctx, "extend_wait_for_lock_hash_pool")
	pool.Lock()
	defer pool.Unlock()
	reg.End()

	// get index of a next test DB - its ID
	index := len(pool.dbs)
	if index == cap(pool.dbs) {
		return db.TestDatabase{}, ErrPoolFull
	}

	// initalization of a new DB
	newTestDB := db.TestDatabase{
		Database: db.Database{
			TemplateHash: pool.templateDB.TemplateHash,
			Config:       pool.templateDB.Config,
		},
		ID: index,
	}

	// set DB name
	dbName := makeDBName(testDBPrefix, pool.templateDB.TemplateHash, index)
	newTestDB.Database.Config.Database = dbName

	templateDB := pool.templateDB.Config.Database
	if err := pool.recreateDB(ctx, newTestDB, templateDB); err != nil {
		return db.TestDatabase{}, err
	}

	// add new test DB to the pool
	pool.dbs = append(pool.dbs, existingDB{state: state, TestDatabase: newTestDB})

	return newTestDB, nil
	// dbHashPool unlocked
	// !
}

func (pool *dbHashPool) removeAll(removeFunc func(db.TestDatabase) error) error {

	// stop the worker
	// we don't close here because if the remove operation fails, we want to be able to repeat it
	for i := 0; i < pool.numOfWorkers; i++ {
		pool.dirty <- stopWorkerMessage
	}
	pool.wg.Wait()

	// !
	// dbHashPool locked
	pool.Lock()
	defer pool.Unlock()

	if len(pool.dbs) == 0 {
		return nil
	}

	// remove from back to be able to repeat operation in case of error
	for id := len(pool.dbs) - 1; id >= 0; id-- {
		testDB := pool.dbs[id].TestDatabase

		if err := removeFunc(testDB); err != nil {
			return err
		}

		if len(pool.dbs) > 1 {
			pool.dbs = pool.dbs[:len(pool.dbs)-1]
		}
	}

	// close all only if removal of all succeeded
	pool.dbs = nil
	close(pool.dirty)

	return nil
	// dbHashPool unlocked
	// !
}

// MakeDBName makes a test DB name with the configured prefix, template hash and ID of the DB.
func (p *DBPool) MakeDBName(hash string, id int) string {
	p.mutex.RLock()
	p.mutex.RUnlock()

	return makeDBName(p.dbNamePrefix, hash, id)
}

func makeDBName(testDBPrefix string, hash string, id int) string {
	// db name has an ID in suffix
	return fmt.Sprintf("%s%s_%03d", testDBPrefix, hash, id)
}
