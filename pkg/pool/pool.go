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
)

type dbState int // Indicates a current DB state.

const (
	dbStateReady dbState = iota // Initialized according to a template and ready to be picked up.
	dbStateDirty                // Taken by a client and potentially currently in use.
)

const minConcurrentTasksNum = 3 // controlLoop + workerTaskLoop + at least one goroutine to handle a task

type existingDB struct {
	state     dbState
	createdAt time.Time
	db.TestDatabase
}

type workerTask string

const (
	workerTaskStop       = "STOP"
	workerTaskExtend     = "EXTEND"
	workerTaskCleanDirty = "CLEAN_DIRTY"
)

// HashPool holds a test DB pool for a certain hash. Each HashPool is running cleanup workers in background.
type HashPool struct {
	dbs   []existingDB
	ready chan int // ID of initalized DBs according to a template, ready to pick them up
	dirty chan int // ID of DBs that were given away and need to be recreated to reuse them

	recreateDB recreateTestDBFunc
	templateDB db.Database
	PoolConfig

	sync.RWMutex
	wg sync.WaitGroup

	tasksChan chan string
	running   bool
}

// NewHashPool creates new hash pool with the given config.
// Starts the workers to extend the pool in background up to requested inital number.
func NewHashPool(cfg PoolConfig, templateDB db.Database, initDBFunc RecreateDBFunc) *HashPool {

	if cfg.MaxConcurrentTasks < minConcurrentTasksNum {
		cfg.MaxConcurrentTasks = minConcurrentTasksNum
	}

	pool := &HashPool{
		dbs:   make([]existingDB, 0, cfg.MaxPoolSize),
		ready: make(chan int, cfg.MaxPoolSize),
		dirty: make(chan int, cfg.MaxPoolSize),

		recreateDB: makeActualRecreateTestDBFunc(templateDB.Config.Database, initDBFunc),
		templateDB: templateDB,
		PoolConfig: cfg,

		tasksChan: make(chan string, cfg.MaxPoolSize+1),
		running:   false,
	}

	return pool
}

func (pool *HashPool) Start() {
	pool.Lock()
	defer pool.Unlock()

	if pool.running {
		return
	}

	pool.running = true
	for i := 0; i < pool.InitialPoolSize; i++ {
		pool.tasksChan <- workerTaskExtend
	}

	pool.wg.Add(1)
	go func() {
		defer pool.wg.Done()
		pool.controlLoop()
	}()
}

func (pool *HashPool) Stop() {
	pool.Lock()
	if !pool.running {
		return
	}
	pool.running = false
	pool.Unlock()

	pool.tasksChan <- workerTaskStop
	pool.wg.Wait()
}

func (pool *HashPool) GetTestDatabase(ctx context.Context, hash string, timeout time.Duration) (db db.TestDatabase, err error) {
	var index int

	// fmt.Printf("pool#%s: waiting for ready ID...\n", hash)

	select {
	case <-time.After(timeout):
		err = ErrTimeout
		return
	case <-ctx.Done():
		err = ctx.Err()
		return
	case index = <-pool.ready:
	}

	// fmt.Printf("pool#%s: got ready ID=%v\n", hash, index)

	reg := trace.StartRegion(ctx, "wait_for_lock_hash_pool")
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
	pool.dirty <- index

	if len(pool.dbs) < pool.PoolConfig.MaxPoolSize {
		pool.tasksChan <- workerTaskExtend
	}

	// we try to ensure that InitialPoolSize count is staying ready
	// thus, we try to move the oldest dirty dbs into cleaning
	if len(pool.dbs) >= pool.PoolConfig.MaxPoolSize {
		pool.tasksChan <- workerTaskCleanDirty
	}

	// fmt.Printf("pool#%s: ready=%d, dirty=%d, waitingForCleaning=%d, dbs=%d initial=%d max=%d (GetTestDatabase)\n", hash, len(pool.ready), len(pool.dirty), len(pool.waitingForCleaning), len(pool.dbs), pool.PoolConfig.InitialPoolSize, pool.PoolConfig.MaxPoolSize)

	return testDB.TestDatabase, nil
}

func (pool *HashPool) AddTestDatabase(ctx context.Context, templateDB db.Database) error {
	return pool.extend(ctx)
}

func (pool *HashPool) workerTaskLoop(ctx context.Context, taskChan <-chan string, maxConcurrentTasks int) {

	handlers := map[string]func(ctx context.Context) error{
		workerTaskExtend:     pool.extendIngoreErrPoolFull,
		workerTaskCleanDirty: pool.cleanDirty,
	}

	// to limit the number of running goroutines.
	var semaphore = make(chan struct{}, pool.MaxConcurrentTasks)

	for task := range taskChan {
		switch task {
		case workerTaskStop:
			return
		default:
			handler, ok := handlers[task]
			if !ok {
				fmt.Printf("invalid task: %s", task)
				continue
			}

			select {
			case <-ctx.Done():
				return
			case semaphore <- struct{}{}:
			}

			pool.wg.Add(1)
			go func(task string) {

				defer func() {
					pool.wg.Done()
					<-semaphore
				}()

				// fmt.Println("task", task)
				if err := handler(ctx); err != nil {
					// fmt.Println("task", task, "failed:", err.Error())
				}
			}(task)
		}
	}
}

func (pool *HashPool) controlLoop() {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workerTasksChan := make(chan string, len(pool.tasksChan))
	pool.wg.Add(1)
	go func() {
		defer pool.wg.Done()
		pool.workerTaskLoop(ctx, workerTasksChan, pool.MaxConcurrentTasks)
	}()

	for task := range pool.tasksChan {
		if task == workerTaskStop {
			cancel()
			workerTasksChan <- task
			return
		}

		select {
		case workerTasksChan <- task:
		default:
			// don't wait until task can be added,
			// be available to receive Stop message at any time
		}
	}
}

func (pool *HashPool) RecreateTestDatabase(ctx context.Context, hash string, id int) error {

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

	// directly change the state to 'ready'
	testDB.state = dbStateReady
	pool.dbs[id] = testDB

	pool.ready <- id

	return nil

}

// cleanDirty reads 'dirty' channel and cleans up a test DB with the received index.
// When the DB is recreated according to a template, its index goes to the 'ready' channel.
// The function waits until there is a dirty DB...
func (pool *HashPool) cleanDirty(ctx context.Context) error {

	ctx, task := trace.NewTask(ctx, "worker_clean_dirty")
	defer task.End()

	var id int
	select {
	case id = <-pool.dirty:
	case <-ctx.Done():
		return ctx.Err()
	default:
		// nothing to do
		return nil
	}

	regLock := trace.StartRegion(ctx, "worker_wait_for_rlock_hash_pool")
	pool.RLock()
	regLock.End()

	if id < 0 || id >= len(pool.dbs) {
		// sanity check, should never happen
		pool.RUnlock()
		return ErrInvalidIndex
	}
	testDB := pool.dbs[id]
	pool.RUnlock()

	if testDB.state == dbStateReady {
		// nothing to do
		return nil
	}

	reg := trace.StartRegion(ctx, "worker_db_operation")
	err := pool.recreateDB(ctx, &testDB)
	reg.End()

	if err != nil {
		fmt.Printf("worker_clean_dirty: failed to clean up DB ID='%v': %v\n", id, err)

		// we guarantee FIFO, we must keeping trying to clean up **exactly this** test database!
		if errors.Is(err, ErrTestDBInUse) {

			fmt.Printf("worker_clean_dirty: scheduling retry cleanup for ID='%v'...\n", id)
			time.Sleep(250 * time.Millisecond)
			fmt.Printf("integworker_clean_dirtyresql: push DB ID='%v' into retry.", id)
			pool.dirty <- id
			pool.tasksChan <- workerTaskCleanDirty
			return nil
		}

		return err
	}

	regLock = trace.StartRegion(ctx, "worker_wait_for_lock_hash_pool")
	pool.Lock()
	defer pool.Unlock()
	regLock.End()

	if testDB.state == dbStateReady {
		// oups, it has been cleaned by another worker already
		// we won't add it to the 'ready' channel to avoid duplication
		return nil
	}

	testDB.state = dbStateReady
	pool.dbs[id] = testDB

	pool.ready <- testDB.ID

	return nil
}

func (pool *HashPool) extendIngoreErrPoolFull(ctx context.Context) error {
	err := pool.extend(ctx)
	if errors.Is(err, ErrPoolFull) {
		return nil
	}

	return err
}

func (pool *HashPool) extend(ctx context.Context) error {

	ctx, task := trace.NewTask(ctx, "worker_extend")
	defer task.End()

	reg := trace.StartRegion(ctx, "worker_wait_for_lock_hash_pool")
	pool.Lock()
	defer pool.Unlock()
	reg.End()

	// get index of a next test DB - its ID
	index := len(pool.dbs)
	if index == cap(pool.dbs) {
		return ErrPoolFull
	}

	// initalization of a new DB using template config
	newTestDB := existingDB{
		state:     dbStateReady,
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

	reg = trace.StartRegion(ctx, "worker_db_operation")
	err := pool.recreateDB(ctx, &newTestDB)
	reg.End()

	if err != nil {
		return err
	}

	// add new test DB to the pool
	pool.dbs = append(pool.dbs, newTestDB)

	pool.ready <- newTestDB.ID

	return nil
}

func (pool *HashPool) RemoveAll(ctx context.Context, removeFunc RemoveDBFunc) error {

	// stop all workers
	pool.Stop()

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
	close(pool.dirty)

	return nil
	// HashPool unlocked
	// !
}
