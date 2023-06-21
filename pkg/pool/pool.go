package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/allaboutapps/integresql/pkg/db"
)

var (
	ErrUnknownHash  = errors.New("no database pool exists for this hash")
	ErrPoolFull     = errors.New("database pool is full")
	ErrInvalidState = errors.New("database state is not valid for this operation")
	ErrInvalidIndex = errors.New("invalid db.Database index (id)")
	ErrTimeout      = errors.New("timeout on waiting for ready db")
)

type dbState int

const (
	dbStateReady = iota
	dbStateInUse = iota
	dbStateDirty = iota
)

type DBPool struct {
	pools map[string]*dbHashPool // map[hash]
	mutex sync.RWMutex

	maxPoolSize int
	wg          sync.WaitGroup
}

func NewDBPool(maxPoolSize int) *DBPool {
	return &DBPool{
		pools: make(map[string]*dbHashPool),

		maxPoolSize: maxPoolSize,
	}
}

type RecreateDBFunc func(ctx context.Context, testDB db.TestDatabase, templateName string) error

type existingDB struct {
	state dbState
	db.TestDatabase
}

type dbHashPool struct {
	dbs   []existingDB
	ready chan int // ID; initalized DBs according to a template, ready to pick them up
	dirty chan int // ID; returned DBs, need to be initalized again to reuse them

	recreateDB RecreateDBFunc
	templateDB db.Database
	sync.RWMutex
}

func newDBHashPool(maxPoolSize int, recreateDB RecreateDBFunc, templateDB db.Database) *dbHashPool {
	return &dbHashPool{
		dbs:        make([]existingDB, 0, maxPoolSize),
		ready:      make(chan int, maxPoolSize),
		dirty:      make(chan int, maxPoolSize),
		recreateDB: recreateDB,
		templateDB: templateDB,
	}
}

func (h *dbHashPool) workerCleanUpDirtyDB() {
	ctx := context.Background()
	templateName := h.templateDB.Config.Database

	for dirtyID := range h.dirty {
		h.RLock()
		if dirtyID >= len(h.dbs) {
			// sanity check, should never happen
			h.RUnlock()
			continue
		}
		testDB := h.dbs[dirtyID]
		h.RUnlock()

		if testDB.state != dbStateDirty {
			continue
		}

		if err := h.recreateDB(ctx, testDB.TestDatabase, templateName); err != nil {
			// TODO anna: error handling
			fmt.Printf("integresql: failed to clean up DB: %v\n", err)
			continue
		}

		h.Lock()
		testDB.state = dbStateReady
		h.dbs[dirtyID] = testDB
		h.Unlock()

		h.ready <- testDB.ID
	}
}

func (p *DBPool) Stop() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	for _, pool := range p.pools {
		close(pool.dirty)
	}
	p.wg.Wait()
	for _, pool := range p.pools {
		close(pool.ready)
	}
}

func (p *DBPool) GetTestDatabase(ctx context.Context, hash string, timeout time.Duration) (db db.TestDatabase, err error) {

	// !
	// DBPool locked
	p.mutex.Lock()
	pool := p.pools[hash]

	if pool == nil {
		// no such pool
		p.mutex.Unlock()
		err = ErrUnknownHash
		return
	}

	p.mutex.Unlock()
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
	pool.Lock()
	defer pool.Unlock()

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

func (pool *dbHashPool) extend(ctx context.Context) (db.TestDatabase, error) {
	// !
	// dbHashPool locked
	pool.Lock()
	defer pool.Unlock()

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
	// db name has an ID in suffix
	templateName := pool.templateDB.Config.Database
	dbName := fmt.Sprintf("%s_%03d", templateName, index)
	newTestDB.Database.Config.Database = dbName

	if err := pool.recreateDB(ctx, newTestDB, templateName); err != nil {
		return db.TestDatabase{}, err
	}

	// add new test DB to the pool
	pool.dbs = append(pool.dbs, existingDB{state: dbStateReady, TestDatabase: newTestDB})

	return newTestDB, nil
	// dbHashPool unlocked
	// !
}

func (p *DBPool) AddTestDatabase(ctx context.Context, templateDB db.Database, initFunc RecreateDBFunc) error {
	hash := templateDB.TemplateHash

	// !
	// DBPool locked
	p.mutex.Lock()
	pool := p.pools[hash]

	if pool == nil {
		// create a new dbHashPool
		pool = newDBHashPool(p.maxPoolSize, initFunc, templateDB)
		// and start the cleaning worker
		p.enableworkerCleanUpDirtyDB(pool)

		// pool is ready
		p.pools[hash] = pool
	}

	p.mutex.Unlock()
	// DBPool unlocked
	// !

	newTestDB, err := pool.extend(ctx)
	if err != nil {
		return err
	}

	// and add its index to 'ready'
	pool.ready <- newTestDB.ID

	return nil
}

func (p *DBPool) ExtendPool(ctx context.Context, templateDB db.Database) (db.TestDatabase, error) {
	hash := templateDB.TemplateHash

	// !
	// DBPool locked
	p.mutex.Lock()
	pool := p.pools[hash]

	if pool == nil {
		// meant to be only for already initialized pools
		p.mutex.Unlock()
		return db.TestDatabase{}, ErrUnknownHash
	}

	p.mutex.Unlock()
	// DBPool unlocked
	// !

	newTestDB, err := pool.extend(ctx)
	if err != nil {
		return db.TestDatabase{}, err
	}

	return newTestDB, nil
}

func (p *DBPool) ReturnTestDatabase(ctx context.Context, hash string, id int) error {

	// !
	// DBPool locked
	p.mutex.Lock()
	pool := p.pools[hash]

	if pool == nil {
		// no such pool
		p.mutex.Unlock()
		return ErrUnknownHash
	}

	// !
	// dbHashPool locked
	pool.Lock()
	defer pool.Unlock()

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

	return p.removeAllFromPool(pool, removeFunc)
	// DBPool unlocked
	// !
}

func (p *DBPool) enableworkerCleanUpDirtyDB(pool *dbHashPool) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		pool.workerCleanUpDirtyDB()
	}()
}

func (p *DBPool) removeAllFromPool(pool *dbHashPool, removeFunc func(db.TestDatabase) error) error {
	// close the channels, and reopen them when the operation is completed
	close(pool.dirty)
	close(pool.ready)

	// !
	// dbHashPool locked
	pool.Lock()
	defer pool.Unlock()

	// remove from back to be able to repeat operation in case of error
	for id := len(pool.dbs) - 1; id >= 0; id-- {
		testDB := pool.dbs[id].TestDatabase

		if err := removeFunc(testDB); err != nil {
			return err
		}

		pool.dbs = pool.dbs[:len(pool.dbs)-1]
	}

	// all DBs removed, enable the worker again
	pool.dirty = make(chan int, p.maxPoolSize)
	pool.ready = make(chan int, p.maxPoolSize)
	p.enableworkerCleanUpDirtyDB(pool)

	return nil
	// dbHashPool unlocked
	// !
}

func (p *DBPool) RemoveAll(ctx context.Context, removeFunc func(db.TestDatabase) error) error {
	// !
	// DBPool locked
	p.mutex.Lock()
	defer p.mutex.Unlock()

	for hash, pool := range p.pools {
		if err := p.removeAllFromPool(pool, removeFunc); err != nil {
			return err
		}

		delete(p.pools, hash)
	}

	return nil
	// DBPool unlocked
	// !
}
