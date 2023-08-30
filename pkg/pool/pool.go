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

const minConcurrentTasksNum = 1

type existingDB struct {
	state dbState
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
	dbs        []existingDB
	ready      chan int      // ID of initalized DBs according to a template, ready to pick them up
	dirty      chan int      // ID of DBs that were given away and need to be recreated to reuse them
	recreating chan struct{} // tracks currently running recreating ops

	recreateDB recreateTestDBFunc
	templateDB db.Database
	PoolConfig

	sync.RWMutex
	wg sync.WaitGroup

	tasksChan     chan string
	running       bool
	workerContext context.Context // the ctx all background workers will receive (nil if not yet started)
}

// NewHashPool creates new hash pool with the given config.
// Starts the workers to extend the pool in background up to requested inital number.
func NewHashPool(cfg PoolConfig, templateDB db.Database, initDBFunc RecreateDBFunc) *HashPool {

	if cfg.MaxParallelTasks < minConcurrentTasksNum {
		cfg.MaxParallelTasks = minConcurrentTasksNum
	}

	pool := &HashPool{
		dbs:        make([]existingDB, 0, cfg.MaxPoolSize),
		ready:      make(chan int, cfg.MaxPoolSize),
		dirty:      make(chan int, cfg.MaxPoolSize),
		recreating: make(chan struct{}, cfg.MaxPoolSize),

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

	ctx, cancel := context.WithCancel(context.Background())
	pool.workerContext = ctx

	for i := 0; i < pool.InitialPoolSize; i++ {
		pool.tasksChan <- workerTaskExtend
	}

	pool.wg.Add(1)
	go func() {
		defer pool.wg.Done()
		pool.controlLoop(ctx, cancel)
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
	pool.workerContext = nil
}

func (pool *HashPool) GetTestDatabase(ctx context.Context, hash string, timeout time.Duration) (db db.TestDatabase, err error) {
	var index int

	fmt.Printf("pool#%s: waiting for ready ID...\n", hash)

	select {
	case <-time.After(timeout):
		err = ErrTimeout
		return
	case <-ctx.Done():
		err = ctx.Err()
		return
	case index = <-pool.ready:
	}

	fmt.Printf("pool#%s: got ready ID=%v\n", hash, index)

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

		fmt.Printf("pool#%s: GetTestDatabase ErrInvalidState ID=%v\n", hash, index)

		err = ErrInvalidState
		return
	}

	testDB.state = dbStateDirty
	pool.dbs[index] = testDB
	pool.dirty <- index

	if len(pool.dbs) < pool.PoolConfig.MaxPoolSize {
		fmt.Printf("pool#%s: Conditional extend\n", hash)
		pool.tasksChan <- workerTaskExtend
	}

	// we try to ensure that InitialPoolSize count is staying ready
	// thus, we try to move the oldest dirty dbs into recreating with the workerTaskCleanDirty
	if len(pool.dbs) >= pool.PoolConfig.MaxPoolSize && (len(pool.ready)+len(pool.recreating)) < pool.InitialPoolSize {
		pool.tasksChan <- workerTaskCleanDirty
	}

	fmt.Printf("pool#%s: ready=%d, dirty=%d, recreating=%d, tasksChan=%d, dbs=%d initial=%d max=%d (GetTestDatabase)\n", pool.templateDB.TemplateHash, len(pool.ready), len(pool.dirty), len(pool.recreating), len(pool.tasksChan), len(pool.dbs), pool.PoolConfig.InitialPoolSize, pool.PoolConfig.MaxPoolSize)

	return testDB.TestDatabase, nil
}

func (pool *HashPool) AddTestDatabase(ctx context.Context, templateDB db.Database) error {
	return pool.extend(ctx)
}

func (pool *HashPool) workerTaskLoop(ctx context.Context, taskChan <-chan string, MaxParallelTasks int) {

	fmt.Printf("pool#%s: workerTaskLoop\n", pool.templateDB.TemplateHash)

	handlers := map[string]func(ctx context.Context) error{
		workerTaskExtend:     ignoreErrs(pool.extend, ErrPoolFull, context.Canceled),
		workerTaskCleanDirty: ignoreErrs(pool.cleanDirty, context.Canceled),
	}

	// to limit the number of running goroutines.
	var semaphore = make(chan struct{}, MaxParallelTasks)

	for task := range taskChan {
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

			fmt.Printf("pool#%s: workerTaskLoop task=%v\n", pool.templateDB.TemplateHash, task)

			if err := handler(ctx); err != nil {
				fmt.Printf("pool#%s: workerTaskLoop task=%v FAILED! err=%v\n", pool.templateDB.TemplateHash, task, err.Error())
			}
		}(task)

	}
}

func (pool *HashPool) controlLoop(ctx context.Context, cancel context.CancelFunc) {

	fmt.Printf("pool#%s: controlLoop\n", pool.templateDB.TemplateHash)

	// ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workerTasksChan := make(chan string, len(pool.tasksChan))
	pool.wg.Add(1)
	go func() {
		defer pool.wg.Done()
		pool.workerTaskLoop(ctx, workerTasksChan, pool.MaxParallelTasks)
	}()

	for task := range pool.tasksChan {
		if task == workerTaskStop {
			close(workerTasksChan)
			cancel()
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

// ReturnTestDatabase returns the given test DB directly to the pool, without cleaning (recreating it).
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

	// remove id from dirty and add it to ready channel
	pool.excludeIDFromDirtyChannel(id)
	pool.ready <- id

	fmt.Printf("pool#%s: ready=%d, dirty=%d, recreating=%d, tasksChan=%d, dbs=%d initial=%d max=%d (ReturnTestDatabase)\n", pool.templateDB.TemplateHash, len(pool.ready), len(pool.dirty), len(pool.recreating), len(pool.tasksChan), len(pool.dbs), pool.PoolConfig.InitialPoolSize, pool.PoolConfig.MaxPoolSize)

	return nil
}

func (pool *HashPool) excludeIDFromDirtyChannel(id int) {

	// The testDB identified by overgiven id may still in the dirty channel. We want to exclude it.
	// We need to explicitly remove it from there by filtering the current channel to a tmp channel.
	// We finally close the tmp channel and flush it onto the dirty channel again.
	// The id is now no longer in the channel.
	filteredDirty := make(chan int, pool.MaxPoolSize)

	var dirtyID int
	for loop := true; loop; {
		select {
		case dirtyID = <-pool.dirty:
			if dirtyID != id {
				filteredDirty <- dirtyID
			}
		default:
			loop = false
			break
		}
	}

	// filteredDirty now has all filtered values without the above id, redirect the other ids back to the dirty channel.
	// close so we can range over it...
	close(filteredDirty)

	for dirtyID := range filteredDirty {
		pool.dirty <- dirtyID
	}
}

// RecreateTestDatabase prioritizes the test DB to be recreated next via the dirty worker.
func (pool *HashPool) RecreateTestDatabase(ctx context.Context, hash string, id int) error {

	pool.RLock()
	if id < 0 || id >= len(pool.dbs) {
		pool.RUnlock()
		return ErrInvalidIndex
	}

	fmt.Printf("pool#%s: ready=%d, dirty=%d, recreating=%d, tasksChan=%d, dbs=%d initial=%d max=%d (RecreateTestDatabase %v)\n", pool.templateDB.TemplateHash, len(pool.ready), len(pool.dirty), len(pool.recreating), len(pool.tasksChan), len(pool.dbs), pool.PoolConfig.InitialPoolSize, pool.PoolConfig.MaxPoolSize, id)
	pool.RUnlock()

	// exclude from the normal dirty channel, force recreation in a background worker...
	pool.excludeIDFromDirtyChannel(id)

	// directly spawn a new worker in the bg (with the same ctx as the typical workers)
	go pool.recreateDatabaseGracefully(pool.workerContext, id)

	return nil
}

// recreateDatabaseGracefully continuosly tries to recreate the testdatabase and will retry/block until it succeeds
func (pool *HashPool) recreateDatabaseGracefully(ctx context.Context, id int) error {

	if ctx.Err() != nil {
		// pool closed in the meantime.
		return ctx.Err()
	}

	pool.RLock()

	if pool.dbs[id].state == dbStateReady {
		// nothing to do
		pool.RUnlock()
		return nil
	}

	testDB := pool.dbs[id]
	pool.RUnlock()

	pool.recreating <- struct{}{}

	defer func() {
		<-pool.recreating
	}()

	try := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			try++

			fmt.Printf("recreateDatabaseGracefully: recreating ID='%v' try=%v...\n", id, try)
			err := pool.recreateDB(ctx, &testDB)
			if err != nil {
				// only still connected errors are worthy a retry
				if errors.Is(err, ErrTestDBInUse) {

					backoff := time.Duration(try) * pool.PoolConfig.TestDatabaseRetryRecreateSleepMin
					if backoff > pool.PoolConfig.TestDatabaseRetryRecreateSleepMax {
						backoff = pool.PoolConfig.TestDatabaseRetryRecreateSleepMax
					}

					fmt.Printf("recreateDatabaseGracefully: DB is still in use, will retry ID='%v' try=%v in backoff=%v.\n", id, try, backoff)
					time.Sleep(backoff)
				} else {
					fmt.Printf("recreateDatabaseGracefully: bailout worker task DB error while cleanup ID='%v' try=%v err=%v\n", id, try, err)
					return err
				}
			} else {
				goto MoveToReady
			}
		}
	}

MoveToReady:
	pool.Lock()
	defer pool.Unlock()

	if ctx.Err() != nil {
		// pool closed in the meantime.
		return ctx.Err()
	}

	fmt.Printf("pool#%s: ready=%d, dirty=%d, recreating=%d, tasksChan=%d, dbs=%d initial=%d max=%d (recreateDatabaseGracefully %v)\n", pool.templateDB.TemplateHash, len(pool.ready), len(pool.dirty), len(pool.recreating), len(pool.tasksChan), len(pool.dbs), pool.PoolConfig.InitialPoolSize, pool.PoolConfig.MaxPoolSize, id)

	if pool.dbs[id].state == dbStateReady {
		// oups, it has been cleaned by another worker already
		// we won't add it to the 'ready' channel to avoid duplication
		return nil
	}

	pool.dbs[id].state = dbStateReady
	// pool.dbs[id] = testDB

	pool.ready <- pool.dbs[id].ID

	return nil
}

// cleanDirty reads 'dirty' channel and cleans up a test DB with the received index.
// When the DB is recreated according to a template, its index goes to the 'ready' channel.
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
		fmt.Println("cleanDirty noop")
		return nil
	}

	fmt.Printf("pool#%s: cleanDirty %v\n", pool.templateDB.TemplateHash, id)

	regLock := trace.StartRegion(ctx, "worker_wait_for_rlock_hash_pool")
	pool.RLock()
	regLock.End()

	if id < 0 || id >= len(pool.dbs) {
		// sanity check, should never happen
		pool.RUnlock()
		return ErrInvalidIndex
	}
	// testDB := pool.dbs[id]
	pool.RUnlock()

	return pool.recreateDatabaseGracefully(ctx, id)
}

func ignoreErrs(f func(ctx context.Context) error, errs ...error) func(context.Context) error {
	return func(ctx context.Context) error {
		err := f(ctx)
		for _, e := range errs {
			if errors.Is(err, e) {
				return nil
			}
		}
		return err
	}
}

func (pool *HashPool) extend(ctx context.Context) error {

	ctx, task := trace.NewTask(ctx, "worker_extend")
	defer task.End()

	reg := trace.StartRegion(ctx, "worker_wait_for_lock_hash_pool")
	pool.Lock()
	defer pool.Unlock()

	fmt.Printf("pool#%s: ready=%d, dirty=%d, recreating=%d, tasksChan=%d, dbs=%d initial=%d max=%d (extend)\n", pool.templateDB.TemplateHash, len(pool.ready), len(pool.dirty), len(pool.recreating), len(pool.tasksChan), len(pool.dbs), pool.PoolConfig.InitialPoolSize, pool.PoolConfig.MaxPoolSize)
	reg.End()

	// get index of a next test DB - its ID
	index := len(pool.dbs)
	if index == cap(pool.dbs) {
		return ErrPoolFull
	}

	// initalization of a new DB using template config
	newTestDB := existingDB{
		state: dbStateReady,
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
	close(pool.tasksChan)

	return nil
}
