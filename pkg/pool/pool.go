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

type existingDB struct {
	state     dbState
	createdAt time.Time
	db.TestDatabase
}

// HashPool holds a test DB pool for a certain hash. Each HashPool is running cleanup workers in background.
type HashPool struct {
	dbs                []existingDB
	ready              chan int // ID of initalized DBs according to a template, ready to pick them up
	waitingForCleaning chan int // ID of returned DBs, need to be recreated to reuse them
	dirty              chan int // ID of DBs that were given away and are currenly in use

	recreateDB recreateTestDBFunc
	templateDB db.Database
	PoolConfig

	sync.RWMutex
	wg sync.WaitGroup
}

// NewHashPool creates new hash pool with the given config.
// If EnableDBRecreate is true, cleanup workers start automatically.
func NewHashPool(cfg PoolConfig, templateDB db.Database, initDBFunc RecreateDBFunc) *HashPool {

	pool := &HashPool{
		dbs:                make([]existingDB, 0, cfg.MaxPoolSize),
		ready:              make(chan int, cfg.MaxPoolSize),
		waitingForCleaning: make(chan int, cfg.MaxPoolSize),
		dirty:              make(chan int, 3*cfg.MaxPoolSize), // here indexes can be duplicated

		recreateDB: makeActualRecreateTestDBFunc(templateDB.Config.Database, initDBFunc),
		templateDB: templateDB,
		PoolConfig: cfg,
	}

	pool.enableWorkers()

	return pool
}

func (pool *HashPool) Stop() {
	close(pool.waitingForCleaning)
	pool.wg.Wait()
}

func (pool *HashPool) GetTestDatabase(ctx context.Context, hash string, timeout time.Duration) (db db.TestDatabase, err error) {

	var index int
	if pool.EnableDBRecreate {
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

	reg := trace.StartRegion(ctx, "wait_for_lock_hash_pool")
	pool.Lock()

	// LEGACY HANDLING: we try to ensure that InitialPoolSize count is staying ready
	// thus, we try to move the oldest dirty dbs into cleaning
	if !pool.EnableDBRecreate && len(pool.dbs) >= pool.PoolConfig.MaxPoolSize {
		go pool.pushNotReturnedDirtyToCleaning()
	}

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

	// LEGACY HANDLING: Always try to extend in the BG until we reach the max pool limit...
	if !pool.EnableDBRecreate && len(pool.dbs) < pool.PoolConfig.MaxPoolSize {

		go func(pool *HashPool, testDBNamePrefix string) {
			// fmt.Printf("pool#%s: bg extend...\n", hash)
			newTestDB, err := pool.extend(context.Background(), dbStateReady)
			if err != nil {
				// fmt.Printf("pool#%s: extend failed with error: %v\n", hash, err)
				return
			}

			// fmt.Printf("pool#%s: extended ID=%v\n", hash, newTestDB.ID)
			pool.ready <- newTestDB.ID
		}(pool, pool.TestDBNamePrefix)
	}

	// fmt.Printf("pool#%s: ready=%d, dirty=%d, waitingForCleaning=%d, dbs=%d initial=%d max=%d (GetTestDatabase)\n", hash, len(pool.ready), len(pool.dirty), len(pool.waitingForCleaning), len(pool.dbs), pool.PoolConfig.InitialPoolSize, pool.PoolConfig.MaxPoolSize)

	return testDB.TestDatabase, nil

}

func (pool *HashPool) AddTestDatabase(ctx context.Context, templateDB db.Database) error {

	newTestDB, err := pool.extend(ctx, dbStateReady)
	if err != nil {
		// if errors.Is(err, ErrPoolFull) && !pool.EnableDBRecreate {
		// 	// we can try to recreate test databases that are 'dirty'
		// 	_, err := pool.recreateDirtyDB(ctx, false /* shouldKeepDirty */)
		// 	return err
		// }

		return err
	}

	// and add its index to 'ready'
	pool.ready <- newTestDB.ID

	return nil
}

func (pool *HashPool) ExtendPool(ctx context.Context, templateDB db.Database) (db.TestDatabase, error) {

	if !pool.EnableDBRecreate {
		return db.TestDatabase{}, ErrUnsupported
	}

	// because we return it right away, we treat it as 'dirty'
	testDB, err := pool.extend(ctx, dbStateDirty)
	if err != nil {
		// if errors.Is(err, ErrPoolFull) && !pool.EnableDBRecreate {
		// 	// we can try to recreate test databases that are 'dirty'
		// 	return pool.recreateDirtyDB(ctx, true /* shouldKeepDirty */)
		// }

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

func (pool *HashPool) RecreateTestDatabase(ctx context.Context, hash string, id int) error {
	reg := trace.StartRegion(ctx, "wait_for_lock_hash_pool")
	pool.Lock()
	defer pool.Unlock()
	reg.End()

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

}

func (pool *HashPool) ReturnTestDatabase(ctx context.Context, hash string, id int) error {
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

func (pool *HashPool) enableWorkers() {

	for i := 0; i < pool.NumOfWorkers; i++ {
		pool.wg.Add(1)
		go func() {
			defer pool.wg.Done()
			pool.workerCleanUpTask()
		}()
	}
}

// workerCleanUpTask reads 'waitingForCleaning' channel and cleans up a test DB with the received index.
// When the DB is recreated according to a template, its index goes to the 'ready' channel.
func (pool *HashPool) workerCleanUpTask() {

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
			fmt.Printf("workerCleanUpReturnedDB: failed to clean up DB ID='%v': %v\n", id, err)
			task.End()

			// LEGACY HANDLING: we guarantee FIFO, we must keeping trying to clean up **exactly this** test database!
			if !pool.EnableDBRecreate && errors.Is(err, ErrTestDBInUse) {

				fmt.Printf("workerCleanUpReturnedDB: scheduling retry cleanup for ID='%v'...\n", id)

				go func(id int) {
					time.Sleep(250 * time.Millisecond)
					fmt.Printf("integworkerCleanUpReturnedDBresql: push DB ID='%v' into retry.", id)
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

func (pool *HashPool) extend(ctx context.Context, state dbState) (db.TestDatabase, error) {
	// !
	// HashPool locked
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
	newTestDB.Database.Config.Database = makeDBName(pool.TestDBNamePrefix, pool.templateDB.TemplateHash, index)

	if err := pool.recreateDB(ctx, &newTestDB); err != nil {
		return db.TestDatabase{}, err
	}

	// add new test DB to the pool
	pool.dbs = append(pool.dbs, newTestDB)

	return newTestDB.TestDatabase, nil
	// HashPool unlocked
	// !
}

// Select a longest issued DB from the dirty channel and push it to the waitingForCleaning channel.
// Wait until there is a dirty DB...
func (pool *HashPool) pushNotReturnedDirtyToCleaning() {

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

func (pool *HashPool) RemoveAll(ctx context.Context, removeFunc RemoveDBFunc) error {

	// stop the worker
	// we don't close here because if the remove operation fails, we want to be able to repeat it
	for i := 0; i < pool.NumOfWorkers; i++ {
		pool.waitingForCleaning <- stopWorkerMessage
	}
	pool.wg.Wait()

	// !
	// HashPool locked
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
	// HashPool unlocked
	// !
}
