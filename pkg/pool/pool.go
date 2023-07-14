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
	dbStateReady              dbState = iota // Initialized according to a template and ready to be picked up.
	dbStateDirty                             // Currently in use.
	dbStateWaitingForCleaning                // Returned to the pool, waiting for the cleaning.
)

const stopWorkerMessage int = -1

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
			fmt.Printf("integresql: failed to clean up DB: %v\n", err)

			task.End()
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

// resetNotReturned recreates one DB that is 'dirty' and to which no db clients are connected (so it can be dropped).
// If shouldKeepDirty is set to true, the DB state remains 'dirty'. Otherwise, it is marked as 'Ready'
// and can be obtained again with GetTestDatabase request - in such case error is nil but returned db.TestDatabase is empty.
func (pool *dbHashPool) resetNotReturned(ctx context.Context, testDBPrefix string, shouldKeepDirty bool) (db.TestDatabase, error) {
	var testDB existingDB
	var index int
	found := false

	// we want to search in loop for a dirty DB that could be reused
	tryTimes := 5
	for i := 0; i < tryTimes; i++ {

		timeout := 100 * time.Millisecond // arbitrary small timeout not to cause deadlock

		select {
		case <-time.After(timeout):
			return db.TestDatabase{}, ErrPoolFull
		case index = <-pool.dirty:
		}

		// !
		// dbHashPool locked
		reg := trace.StartRegion(ctx, "wait_for_lock_hash_pool")
		pool.Lock()
		reg.End()

		// sanity check, should never happen
		if index < 0 || index >= len(pool.dbs) {
			// if something is wrong with the received index, just return, don't try any other time (maybe RemoveAll was requested)
			return db.TestDatabase{}, ErrInvalidIndex
		}

		testDB = pool.dbs[index]
		pool.Unlock()

		if testDB.state == dbStateReady {
			// this DB is 'ready' already, we can skip it and search for a waitingForCleaning one
			continue
		}

		if err := pool.recreateDB(ctx, &testDB); err != nil {
			// this database remains 'dirty'
			select {
			case pool.dirty <- index:
				// sent to dirty without blocking
			default:
				// channel is full
			}
			continue
		}

		found = true
		break
	}

	if !found {
		return db.TestDatabase{}, ErrPoolFull
	}

	pool.Lock()
	defer pool.Unlock()

	if shouldKeepDirty {
		testDB.state = dbStateDirty
		pool.dbs[index] = testDB

		select {
		case pool.dirty <- index:
			// sent to dirty without blocking
		default:
			// channel is full
		}

		return testDB.TestDatabase, nil
	}

	// if shouldKeepDirty is false, we can add this DB to the ready pool
	testDB.state = dbStateReady
	pool.dbs[index] = testDB
	pool.ready <- index

	return db.TestDatabase{}, nil
	// dbHashPool unlocked
	// !
}

func (pool *dbHashPool) removeAll(ctx context.Context, removeFunc RemoveDBFunc) error {

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

		if err := removeFunc(ctx, testDB); err != nil {
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
