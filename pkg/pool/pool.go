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
	ErrTestDBInUse  = errors.New("test database is in use, close the connection before dropping")
	ErrUnsupported  = errors.New("this operation is not supported with the current pooling strategy")
)

type dbState int // Indicates a current DB state.

const (
	dbStateReady              dbState = iota // Initialized according to a template and ready to be picked up.
	dbStateDirty                             // Currently in use.
	dbStateWaitingForCleaning                // Returned to the pool, waiting for the cleaning.
)

const stopWorkerMessage int = -1

type PoolConfig struct {
	MaxPoolSize      int
	InitialPoolSize  int
	TestDBNamePrefix string
	NumOfWorkers     int  // Number of cleaning workers (each hash pool has enables this number of workers)
	ForceDBReturn    bool // Force returning test DB. If set to false, test databases that are 'dirty' can be recycled (in not actually used).
}

type DBPool struct {
	PoolConfig

	pools map[string]*dbHashPool // map[hash]
	mutex sync.RWMutex
}

// forceDBReturn set to false will allow reusing test databases that are marked as 'dirty'.
// Otherwise, test DB has to be returned when no longer needed and there are higher chances of getting ErrPoolFull when requesting a new DB.
func NewDBPool(cfg PoolConfig) *DBPool {
	return &DBPool{
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

type existingDB struct {
	state     dbState
	createdAt time.Time
	db.TestDatabase
}

// dbHashPool holds a test DB pool for a certain hash. Each dbHashPool is running cleanup workers in background.
type dbHashPool struct {
	dbs                []existingDB
	ready              chan int // ID of initalized DBs according to a template, ready to pick them up
	waitingForCleaning chan int // ID of returned DBs, need to be recreated to reuse them
	dirty              chan int // ID of DBs that were given away and are currenly in use

	recreateDB recreateTestDBFunc
	templateDB db.Database

	numOfWorkers  int
	forceDBReturn bool

	sync.RWMutex
	wg sync.WaitGroup
}

// InitHashPool creates a new pool with a given template hash and starts the cleanup workers.
func (p *DBPool) InitHashPool(ctx context.Context, templateDB db.Database, initDBFunc RecreateDBFunc) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	_ = p.initHashPool(ctx, templateDB, initDBFunc)
}

func (p *DBPool) initHashPool(ctx context.Context, templateDB db.Database, initDBFunc RecreateDBFunc) *dbHashPool {
	// create a new dbHashPool
	pool := newDBHashPool(p.PoolConfig, templateDB, initDBFunc)
	// and start the cleaning worker
	pool.enableWorker(p.NumOfWorkers)

	// pool is ready
	p.pools[pool.templateDB.TemplateHash] = pool

	return pool
}

// Stop is used to stop all background workers
func (p *DBPool) Stop() {
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

	// fmt.Printf("pool#%s: ready=%d, dirty=%d, waitingForCleaning=%d, dbs=%d initial=%d max=%d (GetTestDatabase)\n", hash, len(pool.ready), len(pool.dirty), len(pool.waitingForCleaning), len(pool.dbs), p.PoolConfig.InitialPoolSize, p.PoolConfig.MaxPoolSize)

	forceReturn := p.ForceDBReturn

	p.mutex.RUnlock()
	// DBPool unlocked
	// !

	var index int
	if forceReturn {
		select {
		case <-time.After(timeout):
			err = ErrTimeout
			return
		case index = <-pool.ready:
		}
	} else {
		// wait indefinately!
		// fmt.Printf("pool#%s: waiting for ready ID...\n", hash)
		select {
		case <-ctx.Done():
			err = ErrTimeout
			return
		case index = <-pool.ready:
		}

		// fmt.Printf("pool#%s: got ready ID=%v\n", hash, index)
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

		// fmt.Printf("pool#%s: GetTestDatabase ErrInvalidState ID=%v\n", hash, index)

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

	// LEGACY HANDLING: we try to ensure that InitialPoolSize count is staying ready
	// thus, we try to move the oldest dirty dbs into cleaning
	if !forceReturn {
		// pool.Lock()
		if len(pool.dbs) >= p.PoolConfig.MaxPoolSize {
			go pool.pushNotReturnedDirtyToCleaning()
		}
		// pool.Unlock()
	}

	// LEGACY HANDLING: Always try to extend in the BG until we reach the max pool limit...
	if !forceReturn && len(pool.dbs) < p.PoolConfig.MaxPoolSize {

		go func(pool *dbHashPool, testDBNamePrefix string) {
			// fmt.Printf("pool#%s: bg extend...\n", hash)
			newTestDB, err := pool.extend(context.Background(), dbStateReady, testDBNamePrefix)
			if err != nil {
				// fmt.Printf("pool#%s: extend failed with error: %v\n", hash, err)
				return
			}

			// fmt.Printf("pool#%s: extended ID=%v\n", hash, newTestDB.ID)
			pool.ready <- newTestDB.ID
		}(pool, p.PoolConfig.TestDBNamePrefix)
	}

	return testDB.TestDatabase, nil
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

	// fmt.Printf("pool#%s: ready=%d, dirty=%d, waitingForCleaning=%d, dbs=%d initial=%d max=%d (AddTestDatabase)\n", hash, len(pool.ready), len(pool.dirty), len(pool.waitingForCleaning), len(pool.dbs), p.PoolConfig.InitialPoolSize, p.PoolConfig.MaxPoolSize)

	p.mutex.Unlock()
	// DBPool unlocked
	// !

	newTestDB, err := pool.extend(ctx, dbStateReady, p.PoolConfig.TestDBNamePrefix)
	if err != nil {
		return err
	}

	// and add its index to 'ready'
	pool.ready <- newTestDB.ID

	return nil
}

// AddTestDatabase adds a new test DB to the pool, creates it according to the template, and returns it right away to the caller.
// The new test DB is marked as 'IsUse' and won't be picked up with GetTestDatabase, until it's returned to the pool.
func (p *DBPool) ExtendPool(ctx context.Context, templateDB db.Database) (db.TestDatabase, error) {

	if !p.ForceDBReturn {
		return db.TestDatabase{}, ErrUnsupported
	}

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

	// fmt.Printf("pool#%s: ready=%d, dirty=%d, waitingForCleaning=%d, dbs=%d initial=%d max=%d (ExtendPool)\n", hash, len(pool.ready), len(pool.dirty), len(pool.waitingForCleaning), len(pool.dbs), p.PoolConfig.InitialPoolSize, p.PoolConfig.MaxPoolSize)

	p.mutex.Unlock()
	// DBPool unlocked
	// !

	// because we return it right away, we treat it as 'dirty'
	testDB, err := pool.extend(ctx, dbStateDirty, p.PoolConfig.TestDBNamePrefix)
	if err != nil {
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
func (p *DBPool) RestoreTestDatabase(ctx context.Context, hash string, id int) error {

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

	// fmt.Printf("pool#%s: ready=%d, dirty=%d, waitingForCleaning=%d, dbs=%d initial=%d max=%d (RestoreTestDatabase)\n", hash, len(pool.ready), len(pool.dirty), len(pool.waitingForCleaning), len(pool.dbs), p.PoolConfig.InitialPoolSize, p.PoolConfig.MaxPoolSize)

	// !
	// dbHashPool locked
	reg = trace.StartRegion(ctx, "wait_for_lock_hash_pool")
	pool.Lock()
	defer pool.Unlock()
	reg.End()

	p.mutex.Unlock()
	// DBPool unlocked
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

	// fmt.Printf("pool#%s: ready=%d, dirty=%d, waitingForCleaning=%d, dbs=%d initial=%d max=%d (ReturnTestDatabase)\n", hash, len(pool.ready), len(pool.dirty), len(pool.waitingForCleaning), len(pool.dbs), p.PoolConfig.InitialPoolSize, p.PoolConfig.MaxPoolSize)

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

	// fmt.Printf("pool#%s: ready=%d, dirty=%d, waitingForCleaning=%d, dbs=%d initial=%d max=%d (RemoveAllWithHash)\n", hash, len(pool.ready), len(pool.dirty), len(pool.waitingForCleaning), len(pool.dbs), p.PoolConfig.InitialPoolSize, p.PoolConfig.MaxPoolSize)

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

func newDBHashPool(cfg PoolConfig, templateDB db.Database, initDBFunc RecreateDBFunc) *dbHashPool {
	return &dbHashPool{
		dbs:                make([]existingDB, 0, cfg.MaxPoolSize),
		ready:              make(chan int, cfg.MaxPoolSize),
		waitingForCleaning: make(chan int, cfg.MaxPoolSize),
		dirty:              make(chan int, 3*cfg.MaxPoolSize), // here indexes can be duplicated
		recreateDB:         makeActualRecreateTestDBFunc(templateDB.Config.Database, initDBFunc),
		templateDB:         templateDB,
		numOfWorkers:       cfg.NumOfWorkers,
		forceDBReturn:      cfg.ForceDBReturn,
	}
}

func (pool *dbHashPool) enableWorker(numberOfWorkers int) {
	for i := 0; i < numberOfWorkers; i++ {
		pool.wg.Add(1)
		go func() {
			defer pool.wg.Done()
			pool.workerCleanUpReturnedDB()
		}()
	}
}

// workerCleanUpReturnedDB reads 'waitingForCleaning' channel and cleans up a test DB with the received index.
// When the DB is recreated according to a template, its index goes to the 'ready' channel.
func (pool *dbHashPool) workerCleanUpReturnedDB() {

	for id := range pool.waitingForCleaning {
		if id == stopWorkerMessage {
			break
		}

		// fmt.Printf("workerCleanUpReturnedDB %d\n", id)

		ctx, task := trace.NewTask(context.Background(), "worker_cleanup_task")

		regLock := trace.StartRegion(ctx, "worker_wait_for_rlock_hash_pool")
		pool.RLock()
		regLock.End()

		if id < 0 || id >= len(pool.dbs) {
			// sanity check, should never happen
			pool.RUnlock()
			task.End()
			continue
		}
		testDB := pool.dbs[id]
		pool.RUnlock()

		if testDB.state != dbStateWaitingForCleaning {
			task.End()
			continue
		}

		reg := trace.StartRegion(ctx, "worker_cleanup")
		if err := pool.recreateDB(ctx, &testDB); err != nil {
			// TODO anna: error handling
			// fmt.Printf("workerCleanUpReturnedDB: failed to clean up DB ID='%v': %v\n", id, err)
			task.End()

			// LEGACY HANDLING: we guarantee FIFO, we must keeping trying to clean up **exactly this** test database!
			if !pool.forceDBReturn && errors.Is(err, ErrTestDBInUse) {

				// fmt.Printf("workerCleanUpReturnedDB: scheduling retry cleanup for ID='%v'...\n", id)

				go func(id int) {
					time.Sleep(250 * time.Millisecond)
					// fmt.Printf("integworkerCleanUpReturnedDBresql: push DB ID='%v' into retry.", id)
					pool.waitingForCleaning <- id
				}(id)
			}

			continue
		}

		regLock = trace.StartRegion(ctx, "worker_wait_for_lock_hash_pool")
		pool.Lock()
		regLock.End()

		testDB.state = dbStateReady
		pool.dbs[id] = testDB

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

	// initalization of a new DB using template config
	newTestDB := existingDB{
		state:     state,
		createdAt: time.Now(),
		TestDatabase: db.TestDatabase{
			Database: db.Database{
				TemplateHash: pool.templateDB.TemplateHash,
				Config:       pool.templateDB.Config,
			},
			ID: index,
		},
	}
	// set DB name
	newTestDB.Database.Config.Database = makeDBName(testDBPrefix, pool.templateDB.TemplateHash, index)

	if err := pool.recreateDB(ctx, &newTestDB); err != nil {
		return db.TestDatabase{}, err
	}

	// add new test DB to the pool
	pool.dbs = append(pool.dbs, newTestDB)

	return newTestDB.TestDatabase, nil
	// dbHashPool unlocked
	// !
}

// Select a longest issued DB from the dirty channel and push it to the waitingForCleaning channel.
// Wait until there is a dirty DB...
func (pool *dbHashPool) pushNotReturnedDirtyToCleaning() {

	select {
	case id := <-pool.dirty:
		// fmt.Printf("pushNotReturnedDirtyToCleaning %d\n", id)
		pool.Lock()
		defer pool.Unlock()
		testDB := pool.dbs[id]
		testDB.state = dbStateWaitingForCleaning
		pool.dbs[id] = testDB
		pool.waitingForCleaning <- id
	default:
		// noop
	}
}

func (pool *dbHashPool) removeAll(removeFunc func(db.TestDatabase) error) error {

	// stop the worker
	// we don't close here because if the remove operation fails, we want to be able to repeat it
	for i := 0; i < pool.numOfWorkers; i++ {
		pool.waitingForCleaning <- stopWorkerMessage
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
	close(pool.waitingForCleaning)

	return nil
	// dbHashPool unlocked
	// !
}

// MakeDBName makes a test DB name with the configured prefix, template hash and ID of the DB.
func (p *DBPool) MakeDBName(hash string, id int) string {
	p.mutex.RLock()
	p.mutex.RUnlock()

	return makeDBName(p.PoolConfig.TestDBNamePrefix, hash, id)
}

func makeDBName(testDBPrefix string, hash string, id int) string {
	// db name has an ID in suffix
	return fmt.Sprintf("%s%s_%03d", testDBPrefix, hash, id)
}
