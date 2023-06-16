package manager

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrPoolEmpty    = errors.New("no database exists for this hash")
	ErrPoolFull     = errors.New("database pool is full")
	ErrNotInPool    = errors.New("database is not in the pool")
	ErrNoDBReady    = errors.New("no database is currently ready, perhaps you need to create one")
	ErrInvalidIndex = errors.New("invalid index == poor implementation :(")
)

type DBPool struct {
	pool  map[string]*dbHashPool  // map[hash]
	ready map[string][]int        // map[hash]
	dirty map[string]map[int]bool // map[hash]
	sync.RWMutex

	maxPoolSize int
}

func NewDBPool(maxPoolSize int) *DBPool {
	return &DBPool{
		pool:        make(map[string]*dbHashPool),
		ready:       make(map[string][]int),
		dirty:       make(map[string]map[int]bool),
		maxPoolSize: maxPoolSize,
	}
}

type dbHashPool struct {
	dbs []TestDatabase

	sync.RWMutex
}

func newDBHashPool(maxPoolSize int) *dbHashPool {
	return &dbHashPool{
		dbs: make([]TestDatabase, 0, maxPoolSize),
	}
}

func (p *DBPool) GetReadyDB(ctx context.Context, hash string) (TestDatabase, error) {
	var hashDBs *dbHashPool
	var index int

	// !
	// DBPool locked
	{
		p.Lock()
		defer p.Unlock()

		ready := p.ready[hash]
		if len(ready) == 0 {
			return TestDatabase{}, ErrNoDBReady
		}

		// get and remove last 'ready' index
		index = ready[len(ready)-1]
		ready = ready[:len(ready)-1]
		p.ready[hash] = ready

		// sanity check, should never happen
		if index >= p.maxPoolSize {
			return TestDatabase{}, ErrInvalidIndex
		}

		hashDBs = p.pool[hash]
		if hashDBs == nil {
			// should not happen
			return TestDatabase{}, ErrPoolEmpty
		}

		// add the index to 'dirty'
		dirty := p.dirty[hash]
		if dirty == nil {
			dirty = make(map[int]bool)
		}
		dirty[index] = true
		p.dirty[hash] = dirty

		// !
		// dbHashPool locked before unlocking DBPool
		hashDBs.Lock()
	}
	// DBPool unlocked
	// !
	defer hashDBs.Unlock()

	// pick a ready test database from the index
	if len(hashDBs.dbs) <= index {
		return TestDatabase{}, ErrInvalidIndex
	}

	return hashDBs.dbs[index], nil
	// dbHashPool unlocked
	// !
}

func (p *DBPool) AddTestDatabase(ctx context.Context, template TemplateConfig, dbNamePrefix string) (TestDatabase, error) {
	var newTestDB TestDatabase
	hash := template.TemplateHash

	// !
	// DBPool locked
	{
		p.Lock()
		defer p.Unlock()

		hashDBs := p.pool[hash]
		if hashDBs == nil {
			hashDBs = newDBHashPool(p.maxPoolSize)
			p.pool[hash] = hashDBs
		}

		// !
		// dbHashPool locked
		hashDBs.Lock()
		defer hashDBs.Unlock()

		// get index of a next test DB - its ID
		index := len(hashDBs.dbs)
		if index >= p.maxPoolSize {
			return TestDatabase{}, ErrPoolFull
		}

		newTestDB = TestDatabase{
			Database: Database{
				TemplateHash: template.TemplateHash,
				Config:       template.Config,
			},
			ID: index,
		}
		// db name has an ID in suffix
		dbName := fmt.Sprintf("%s%03d", dbNamePrefix, index)
		newTestDB.Database.Config.Database = dbName

		// add new test DB to the pool
		hashDBs.dbs = append(hashDBs.dbs, newTestDB)

		// and add its index to 'ready'
		ready := p.ready[hash]
		if ready == nil {
			ready = make([]int, 0, p.maxPoolSize)
		}
		ready = append(ready, index)
		p.ready[hash] = ready

		hashDBs.Lock()
	}
	// dbHashPool unlocked
	// !
	// DBPool unlocked
	// !

	return newTestDB, nil
}

func (p *DBPool) ReturnTestDatabase(ctx context.Context, hash string, id int) error {

	// !
	// DBPool locked
	{
		p.Lock()
		defer p.Unlock()

		dirty := p.dirty[hash]
		if len(dirty) == 0 {
			return ErrNotInPool
		}

		exists := dirty[id]
		if !exists {
			return ErrNotInPool
		}

		// p.ready

	}

}
