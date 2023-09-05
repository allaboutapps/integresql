package pool

import (
	"context"
	"errors"
	"runtime/trace"
	"sync"
	"time"

	"github.com/allaboutapps/integresql/pkg/db"
	"github.com/allaboutapps/integresql/pkg/util"
	"github.com/rs/zerolog"
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
	dbStateReady      dbState = iota // Initialized according to a template and ready to be picked up.
	dbStateDirty                     // Taken by a client and potentially currently in use.
	dbStateRecreating                // In the process of being recreated (to prevent concurrent cleans)
)

type existingDB struct {
	state dbState
	db.TestDatabase

	// To prevent auto-cleans of a testdatabase on the dirty channel directly after it was issued as ready,
	// each testdatabase gets a timestamp assigned after which auto-cleaning it generally allowed (unlock
	// and recreate do not respect this). This timeout is typically very low and should only be neccessary
	// to be tweaked in scenarios in which the pool is overloaded by requests.
	// Prefer to tweak InitialPoolSize (the always ready dbs) and MaxPoolSize instead if you have issues here.
	blockAutoCleanDirtyUntil time.Time

	// increased after each recreation, useful for sleepy recreating workers to check if we still operate on the same gen.
	generation uint
}

type workerTask string

const (
	workerTaskStop           = "STOP"
	workerTaskExtend         = "EXTEND"
	workerTaskAutoCleanDirty = "CLEAN_DIRTY"
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

	tasksChan     chan workerTask
	running       bool
	workerContext context.Context // the ctx all background workers will receive (nil if not yet started)
}

// NewHashPool creates new hash pool with the given config.
// Starts the workers to extend the pool in background up to requested inital number.
func NewHashPool(cfg PoolConfig, templateDB db.Database, initDBFunc RecreateDBFunc) *HashPool {

	pool := &HashPool{
		dbs:        make([]existingDB, 0, cfg.MaxPoolSize),
		ready:      make(chan int, cfg.MaxPoolSize),
		dirty:      make(chan int, cfg.MaxPoolSize),
		recreating: make(chan struct{}, cfg.MaxPoolSize),

		recreateDB: makeActualRecreateTestDBFunc(templateDB.Config.Database, initDBFunc),
		templateDB: templateDB,
		PoolConfig: cfg,

		tasksChan: make(chan workerTask, cfg.MaxPoolSize+1),
		running:   false,
	}

	return pool
}

func (pool *HashPool) Start() {

	log := pool.getPoolLogger(context.Background(), "Start")
	pool.Lock()
	log.Debug().Msg("starting...")

	defer pool.Unlock()

	if pool.running {
		log.Warn().Msg("bailout already running!")
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

	log.Info().Msg("started!")
}

func (pool *HashPool) Stop() {

	log := pool.getPoolLogger(context.Background(), "Stop")
	log.Debug().Msg("stopping...")

	pool.Lock()
	if !pool.running {
		log.Warn().Msg("bailout already stopped!")
		return
	}
	pool.running = false
	pool.Unlock()

	pool.tasksChan <- workerTaskStop
	pool.wg.Wait()
	pool.workerContext = nil
	log.Warn().Msg("stopped!")
}

func (pool *HashPool) GetTestDatabase(ctx context.Context, timeout time.Duration) (db db.TestDatabase, err error) {
	var index int

	log := pool.getPoolLogger(ctx, "GetTestDatabase")
	log.Trace().Msg("waiting for ready ID...")

	select {
	case <-time.After(timeout):
		err = ErrTimeout
		log.Error().Err(err).Dur("timeout", timeout).Msg("timeout")
		return
	case <-ctx.Done():
		err = ctx.Err()
		log.Warn().Err(err).Msg("ctx done")
		return
	case index = <-pool.ready:
	}

	log = log.With().Int("id", index).Logger()
	log.Trace().Msg("got ready testdatabase!")

	reg := trace.StartRegion(ctx, "wait_for_lock_hash_pool")
	pool.Lock()
	defer pool.Unlock()
	reg.End()

	// sanity check, should never happen
	if index < 0 || index >= len(pool.dbs) {
		err = ErrInvalidIndex
		log.Error().Err(err).Int("dbs", len(pool.dbs)).Msg("index out of bounds!")
		return
	}

	testDB := pool.dbs[index]
	// sanity check, should never happen - we got this index from 'ready' channel
	if testDB.state != dbStateReady {
		err = ErrInvalidState
		log.Error().Err(err).Msgf("testdatabase is not in ready state=%v!", testDB.state)
		return
	}

	// flag as dirty and block auto clean until
	testDB.state = dbStateDirty
	testDB.blockAutoCleanDirtyUntil = time.Now().Add(pool.TestDatabaseMinimalLifetime)

	pool.dbs[index] = testDB
	pool.dirty <- index

	if len(pool.dbs) < pool.PoolConfig.MaxPoolSize {
		log.Trace().Msg("push workerTaskExtend")
		pool.tasksChan <- workerTaskExtend
	}

	// we try to ensure that InitialPoolSize count is staying ready
	// thus, we try to move the oldest dirty dbs into recreating with the workerTaskAutoCleanDirty
	if len(pool.dbs) >= pool.PoolConfig.MaxPoolSize && (len(pool.ready)+len(pool.recreating)) < pool.InitialPoolSize {
		log.Trace().Msg("push workerTaskAutoCleanDirty")
		pool.tasksChan <- workerTaskAutoCleanDirty
	}

	pool.unsafeTraceLogStats(log)

	return testDB.TestDatabase, nil
}

func (pool *HashPool) workerTaskLoop(ctx context.Context, taskChan <-chan workerTask, MaxParallelTasks int) {

	log := pool.getPoolLogger(ctx, "workerTaskLoop")
	log.Debug().Msg("starting...")

	handlers := map[workerTask]func(ctx context.Context) error{
		workerTaskExtend:         ignoreErrs(pool.extend, ErrPoolFull, context.Canceled),
		workerTaskAutoCleanDirty: ignoreErrs(pool.autoCleanDirty, context.Canceled),
	}

	// to limit the number of running goroutines.
	var semaphore = make(chan struct{}, MaxParallelTasks)

	for task := range taskChan {
		handler, ok := handlers[task]
		if !ok {
			log.Error().Msgf("invalid task: %s", task)
			continue
		}

		select {
		case <-ctx.Done():
			log.Warn().Err(ctx.Err()).Msg("ctx done!")
			return
		case semaphore <- struct{}{}:
		}

		pool.wg.Add(1)
		go func(task workerTask) {

			defer func() {
				pool.wg.Done()
				<-semaphore
			}()

			log.Debug().Msgf("task=%v", task)

			if err := handler(ctx); err != nil {
				log.Error().Err(err).Msgf("task=%v FAILED!", task)
			}
		}(task)

	}
}

func (pool *HashPool) controlLoop(ctx context.Context, cancel context.CancelFunc) {

	log := pool.getPoolLogger(ctx, "controlLoop")
	log.Debug().Msg("starting...")

	defer cancel()

	workerTasksChan := make(chan workerTask, len(pool.tasksChan))
	pool.wg.Add(1)
	go func() {
		defer pool.wg.Done()
		pool.workerTaskLoop(ctx, workerTasksChan, pool.MaxParallelTasks)
	}()

	for task := range pool.tasksChan {
		if task == workerTaskStop {
			log.Debug().Msg("stopping...")
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
func (pool *HashPool) ReturnTestDatabase(ctx context.Context, id int) error {

	log := pool.getPoolLogger(ctx, "ReturnTestDatabase").With().Int("id", id).Logger()
	log.Debug().Msg("returning...")

	pool.Lock()
	defer pool.Unlock()

	if err := ctx.Err(); err != nil {
		// client vanished
		log.Warn().Err(err).Msg("bailout client vanished!")
		return err
	}

	if id < 0 || id >= len(pool.dbs) {
		log.Warn().Int("dbs", len(pool.dbs)).Msg("bailout invalid index!")
		return ErrInvalidIndex
	}

	// check if db is in the correct state
	testDB := pool.dbs[id]
	if testDB.state != dbStateDirty {
		log.Warn().Int("dbs", len(pool.dbs)).Msgf("bailout invalid state=%v.", testDB.state)
		return nil
	}

	// directly change the state to 'ready'
	testDB.state = dbStateReady
	pool.dbs[id] = testDB

	// remove id from dirty and add it to ready channel
	pool.excludeIDFromChannel(pool.dirty, id)
	pool.ready <- id

	pool.unsafeTraceLogStats(log)

	return nil
}

func (pool *HashPool) excludeIDFromChannel(ch chan int, excludeID int) {

	// The testDB identified by overgiven id may still in a specific channel (typically dirty). We want to exclude it.
	// We need to explicitly remove it from there by filtering the current channel to a tmp channel.
	// We finally close the tmp channel and flush it onto the specific channel again.
	// The id is now no longer in the channel.
	filtered := make(chan int, pool.MaxPoolSize)

	var id int
	for loop := true; loop; {
		select {
		case id = <-ch:
			if id != excludeID {
				filtered <- id
			}
		default:
			loop = false
			break
		}
	}

	// filtered now has all filtered values without the above id, redirect the other ids back to the specific channel.
	// close so we can range over it...
	close(filtered)

	for id := range filtered {
		ch <- id
	}
}

// RecreateTestDatabase prioritizes the test DB to be recreated next via the dirty worker.
func (pool *HashPool) RecreateTestDatabase(ctx context.Context, id int) error {

	log := pool.getPoolLogger(ctx, "RecreateTestDatabase").With().Int("id", id).Logger()
	log.Debug().Msg("flag testdatabase for recreation...")

	pool.RLock()

	if id < 0 || id >= len(pool.dbs) {
		log.Warn().Int("dbs", len(pool.dbs)).Msg("bailout invalid index!")
		pool.RUnlock()
		return ErrInvalidIndex
	}

	pool.RUnlock()

	if err := ctx.Err(); err != nil {
		// client vanished
		log.Warn().Err(err).Msg("bailout client vanished!")
		return err
	}

	// exclude from the normal dirty channel, force recreation in a background worker...
	pool.excludeIDFromChannel(pool.dirty, id)

	// directly spawn a new worker in the bg (with the same ctx as the typical workers)
	// note that this runs unchained, meaning we do not care about errors that may happen via this bg task
	//nolint:errcheck
	go pool.recreateDatabaseGracefully(pool.workerContext, id)

	pool.unsafeTraceLogStats(log)
	return nil
}

// recreateDatabaseGracefully continuosly tries to recreate the testdatabase and will retry/block until it succeeds
func (pool *HashPool) recreateDatabaseGracefully(ctx context.Context, id int) error {

	log := pool.getPoolLogger(ctx, "recreateDatabaseGracefully").With().Int("id", id).Logger()
	log.Debug().Msg("recreating...")

	if err := ctx.Err(); err != nil {
		// pool closed in the meantime.
		log.Error().Err(err).Msg("bailout pre locking ctx err")
		return err
	}

	pool.Lock()

	if state := pool.dbs[id].state; state != dbStateDirty {
		// nothing to do
		log.Error().Msgf("bailout not dbStateDirty state=%v", state)
		pool.Unlock()
		return nil
	}

	testDB := pool.dbs[id]

	// set state recreating...
	pool.dbs[id].state = dbStateRecreating
	pool.dbs[id] = testDB

	pool.Unlock()

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

			log.Trace().Int("try", try).Msg("trying to recreate...")
			err := pool.recreateDB(ctx, &testDB)
			if err != nil {
				// only still connected errors are worthy a retry
				if errors.Is(err, ErrTestDBInUse) {

					backoff := time.Duration(try) * pool.PoolConfig.TestDatabaseRetryRecreateSleepMin
					if backoff > pool.PoolConfig.TestDatabaseRetryRecreateSleepMax {
						backoff = pool.PoolConfig.TestDatabaseRetryRecreateSleepMax
					}

					log.Warn().Int("try", try).Dur("backoff", backoff).Msg("DB is still in use, will retry...")
					time.Sleep(backoff)
				} else {

					log.Error().Int("try", try).Err(err).Msg("bailout worker task DB error while cleanup!")
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

	if pool.dbs[id].state == dbStateReady {
		// oups, it has been cleaned by another worker already
		// we won't add it to the 'ready' channel to avoid duplication
		log.Warn().Msg("bailout DB has be cleaned by another worker as its already ready, skipping readd to ready channel!")
		return nil
	}

	// increase the generation of the testdb (as we just recreated it) and move into ready!
	pool.dbs[id].generation++
	pool.dbs[id].state = dbStateReady

	pool.ready <- pool.dbs[id].ID

	log.Debug().Uint("generation", pool.dbs[id].generation).Msg("ready")
	pool.unsafeTraceLogStats(log)
	return nil
}

// autoCleanDirty reads 'dirty' channel and cleans up a test DB with the received index.
// When the DB is recreated according to a template, its index goes to the 'ready' channel.
// Note that we generally gurantee FIFO when it comes to auto-cleaning as long as no manual unlock/recreates happen.
func (pool *HashPool) autoCleanDirty(ctx context.Context) error {

	log := pool.getPoolLogger(ctx, "autoCleanDirty")
	log.Trace().Msg("autocleaning...")

	ctx, task := trace.NewTask(ctx, "worker_clean_dirty")
	defer task.End()

	var id int
	select {
	case id = <-pool.dirty:
	case <-ctx.Done():
		return ctx.Err()
	default:
		// nothing to do
		log.Trace().Msg("noop")
		return nil
	}

	// got id...
	log = log.With().Int("id", id).Logger()
	log.Trace().Msg("checking cleaning prerequisites...")

	regLock := trace.StartRegion(ctx, "worker_wait_for_rlock_hash_pool")
	pool.RLock()
	regLock.End()

	if id < 0 || id >= len(pool.dbs) {
		// sanity check, should never happen
		log.Warn().Int("dbs", len(pool.dbs)).Msg("bailout invalid index!")
		pool.RUnlock()
		return ErrInvalidIndex
	}

	blockedUntil := time.Until(pool.dbs[id].blockAutoCleanDirtyUntil)
	generation := pool.dbs[id].generation

	log = log.With().Dur("blockedUntil", blockedUntil).Uint("generation", generation).Logger()

	pool.RUnlock()

	// immediately pass to pool recreate
	if blockedUntil <= 0 {
		log.Trace().Msg("clean now (immediate)!")
		return pool.recreateDatabaseGracefully(ctx, id)
	}

	// else we need to wait until we are allowed to work with it!
	// we block auto-cleaning until we are allowed to...
	log.Warn().Msg("sleeping before being allowed to clean...")
	time.Sleep(blockedUntil)

	// we need to check that the testDB.generation did not change since we slept
	// (which would indicate that the database was already unlocked/recreated by someone else in the meantime)
	pool.RLock()

	if pool.dbs[id].generation != generation || pool.dbs[id].state != dbStateDirty {
		log.Error().Msgf("bailout old generation=%v vs new generation=%v state=%v", generation, pool.dbs[id].generation, pool.dbs[id].state)
		pool.RUnlock()
		return nil
	}

	pool.RUnlock()

	log.Trace().Msg("clean now (after sleep has happenend)!")
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

	log := pool.getPoolLogger(ctx, "extend")
	log.Trace().Msg("extending...")

	ctx, task := trace.NewTask(ctx, "worker_extend")
	defer task.End()

	reg := trace.StartRegion(ctx, "worker_wait_for_lock_hash_pool")
	pool.Lock()
	defer pool.Unlock()

	reg.End()

	// get index of a next test DB - its ID
	index := len(pool.dbs)
	if index == cap(pool.dbs) {
		log.Error().Int("dbs", len(pool.dbs)).Int("cap", cap(pool.dbs)).Err(ErrPoolFull).Msg("pool is full")
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

	log.Trace().Int("id", index).Msg("adding...")

	reg = trace.StartRegion(ctx, "worker_db_operation")
	err := pool.recreateDB(ctx, &newTestDB)
	reg.End()

	if err != nil {
		return err
	}

	// add new test DB to the pool
	pool.dbs = append(pool.dbs, newTestDB)

	pool.ready <- newTestDB.ID

	pool.unsafeTraceLogStats(log)

	return nil
}

func (pool *HashPool) RemoveAll(ctx context.Context, removeFunc RemoveDBFunc) error {

	log := pool.getPoolLogger(ctx, "RemoveAll")

	// stop all workers
	pool.Stop()

	// wait until all current "recreating" tasks are finished...

	pool.Lock()
	defer pool.Unlock()

	if len(pool.dbs) == 0 {
		log.Error().Msg("bailout no dbs.")
		return nil
	}

	// remove from back to be able to repeat operation in case of error
	for id := len(pool.dbs) - 1; id >= 0; id-- {
		testDB := pool.dbs[id].TestDatabase

		if err := removeFunc(ctx, testDB); err != nil {
			log.Error().Int("id", id).Err(err).Msg("removeFunc testdatabase err")
			return err
		}

		if len(pool.dbs) > 1 {
			pool.dbs = pool.dbs[:len(pool.dbs)-1]
		}

		pool.excludeIDFromChannel(pool.dirty, id)
		pool.excludeIDFromChannel(pool.ready, id)
		log.Debug().Int("id", id).Msg("testdatabase removed!")
	}

	// close all only if removal of all succeeded
	pool.dbs = nil
	close(pool.tasksChan)

	pool.unsafeTraceLogStats(log)

	return nil
}

func (pool *HashPool) getPoolLogger(ctx context.Context, poolFunction string) zerolog.Logger {
	return util.LogFromContext(ctx).With().Str("poolHash", pool.templateDB.TemplateHash).Str("poolFn", poolFunction).Logger()
}

// unsafeTraceLogStats logs stats of this pool. Attention: pool should be read or write locked!
func (pool *HashPool) unsafeTraceLogStats(log zerolog.Logger) {
	log.Trace().Int("ready", len(pool.ready)).Int("dirty", len(pool.dirty)).Int("recreating", len(pool.recreating)).Int("tasksChan", len(pool.tasksChan)).Int("dbs", len(pool.dbs)).Int("initial", pool.PoolConfig.InitialPoolSize).Int("max", pool.PoolConfig.MaxPoolSize).Msg("pool stats")
}
