package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/allaboutapps/integresql/pkg/db"
)

var (
	ErrUnknownHash  = errors.New("no db.Database exists for this hash")
	ErrPoolFull     = errors.New("database pool is full")
	ErrUnknownID    = errors.New("database is not in the pool")
	ErrNoDBReady    = errors.New("no db.Database is currently ready, perhaps you need to create one")
	ErrInvalidIndex = errors.New("invalid db.Database index (ID)")
)

type DBPool struct {
	pools map[string]*dbHashPool // map[hash]
	mutex sync.RWMutex

	maxPoolSize int
}

type dbIDMap map[int]bool // map[db ID]

func NewDBPool(maxPoolSize int) *DBPool {
	return &DBPool{
		pools: make(map[string]*dbHashPool),

		maxPoolSize: maxPoolSize,
	}
}

type dbHashPool struct {
	dbs   []db.TestDatabase
	ready dbIDMap // initalized DBs according to a template, ready to pick them up
	dirty dbIDMap // returned DBs, need to be initalized again to reuse them

	sync.RWMutex
}

func newDBHashPool(maxPoolSize int) *dbHashPool {
	return &dbHashPool{
		dbs:   make([]db.TestDatabase, 0, maxPoolSize),
		ready: make(dbIDMap),
		dirty: make(dbIDMap),
	}
}

func popFirstKey(idMap dbIDMap) int {
	id := -1
	for key := range idMap {
		id = key
		break
	}
	delete(idMap, id)
	return id
}

func (p *DBPool) GetDB(ctx context.Context, hash string) (db db.TestDatabase, isDirty bool, err error) {
	var pool *dbHashPool

	{
		// !
		// DBPool locked
		p.mutex.Lock()
		defer p.mutex.Unlock()

		pool = p.pools[hash]

		if pool == nil {
			// no such pool
			err = ErrUnknownHash
			return
		}

		// !
		// dbHashPool locked
		pool.Lock()

		// DBPool unlocked
		// !
	}
	defer pool.Unlock()

	var index int
	if len(pool.ready) > 0 {
		// if there are some ready to be used DB, just get one
		index = popFirstKey(pool.ready)
	} else {
		// if no DBs are ready, reuse the dirty ones
		if len(pool.dirty) == 0 {
			err = ErrNoDBReady
			return
		}

		isDirty = true
		index = popFirstKey(pool.dirty)
	}

	// sanity check, should never happen
	if index < 0 || index >= p.maxPoolSize {
		err = ErrInvalidIndex
		return
	}

	// pick a ready test db.Database from the index
	if len(pool.dbs) <= index {
		err = ErrInvalidIndex
		return
	}

	return pool.dbs[index], isDirty, nil
	// dbHashPool unlocked
	// !

}

func (p *DBPool) AddTestDatabase(ctx context.Context, template db.Database, dbNamePrefix string, initFunc func(db.TestDatabase) error) (db.TestDatabase, error) {
	var pool *dbHashPool
	hash := template.TemplateHash

	{
		// !
		// DBPool locked
		p.mutex.Lock()
		defer p.mutex.Unlock()

		pool = p.pools[hash]
		if pool == nil {
			pool = newDBHashPool(p.maxPoolSize)
			p.pools[hash] = pool
		}
		// DBPool unlocked
		// !
	}

	// !
	// dbHashPool locked
	pool.Lock()
	defer pool.Unlock()

	// get index of a next test DB - its ID
	index := len(pool.dbs)
	if index >= p.maxPoolSize {
		return db.TestDatabase{}, ErrPoolFull
	}

	// initalization of a new DB
	newTestDB := db.TestDatabase{
		Database: db.Database{
			TemplateHash: template.TemplateHash,
			Config:       template.Config,
		},
		ID: index,
	}
	// db name has an ID in suffix
	dbName := fmt.Sprintf("%s%03d", dbNamePrefix, index)
	newTestDB.Database.Config.Database = dbName

	if err := initFunc(newTestDB); err != nil {
		return db.TestDatabase{}, err
	}

	// add new test DB to the pool
	pool.dbs = append(pool.dbs, newTestDB)

	// and add its index to 'ready'
	pool.ready[index] = true

	return newTestDB, nil
	// dbHashPool unlocked
	// !
}

func (p *DBPool) ReturnTestDatabase(ctx context.Context, hash string, id int) error {
	var pool *dbHashPool

	{
		// !
		// DBPool locked
		p.mutex.Lock()
		defer p.mutex.Unlock()

		// needs to be checked inside locked region
		// because we access maxPoolSize
		if id < 0 || id >= p.maxPoolSize {
			return ErrInvalidIndex
		}

		pool = p.pools[hash]

		if pool == nil {
			// no such pool
			return ErrUnknownHash
		}

		// !
		// dbHashPool locked
		pool.Lock()

		// DBPool unlocked
		// !
	}
	defer pool.Unlock()

	// check if pool has been already returned
	if pool.dirty != nil && len(pool.dirty) > 0 {
		exists := pool.dirty[id]
		if exists {
			return ErrUnknownID
		}
	}

	// ok, it hasn't been returned yet
	pool.dirty[id] = true

	return nil
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

func (p *DBPool) removeAllFromPool(pool *dbHashPool, removeFunc func(db.TestDatabase) error) error {
	pool.Lock()
	defer pool.Unlock()

	// remove from back to be able to repeat operation in case of error
	for id := len(pool.dbs) - 1; id >= 0; id-- {
		db := pool.dbs[id]

		if err := removeFunc(db); err != nil {
			return err
		}

		pool.dbs = pool.dbs[:len(pool.dbs)-1]
		delete(pool.dirty, id)
		delete(pool.ready, id)
	}

	return nil
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
