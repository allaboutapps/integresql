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
	ErrInvalidIndex = errors.New("invalid database index (ID)")
)

type DBPool struct {
	pool  map[string]*dbHashPool // map[hash]
	ready map[string]dbIDMap     // map[hash], initalized DBs according to a template, ready to pick them up
	dirty map[string]dbIDMap     // map[hash], returned DBs, need to be initalized again to reuse them
	sync.RWMutex

	maxPoolSize int
}

type dbIDMap map[int]bool // map[db ID]

func NewDBPool(maxPoolSize int) *DBPool {
	return &DBPool{
		pool: make(map[string]*dbHashPool),

		ready: make(map[string]dbIDMap),
		dirty: make(map[string]dbIDMap),

		maxPoolSize: maxPoolSize,
	}
}

type dbHashPool struct {
	dbs []TestDatabase
}

func newDBHashPool(maxPoolSize int) *dbHashPool {
	return &dbHashPool{
		dbs: make([]TestDatabase, 0, maxPoolSize),
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

func (p *DBPool) GetDB(ctx context.Context, hash string) (db TestDatabase, isDirty bool, err error) {
	var hashDBs *dbHashPool
	var index int

	// !
	// DBPool locked
	p.Lock()
	defer p.Unlock()

	ready := p.ready[hash]
	if len(ready) > 0 {
		// if there are some ready to be used DB, just get one
		index = popFirstKey(ready)
		p.ready[hash] = ready
	} else {
		// if no DBs are ready, reuse the dirty ones
		dirty := p.dirty[hash]
		if len(dirty) == 0 {
			return TestDatabase{}, false, ErrNoDBReady
		}

		isDirty = true
		index = popFirstKey(dirty)
		p.dirty[hash] = dirty
	}

	// sanity check, should never happen
	if index < 0 || index >= p.maxPoolSize {
		return TestDatabase{}, false, ErrInvalidIndex
	}

	hashDBs = p.pool[hash]
	if hashDBs == nil {
		// should not happen
		return TestDatabase{}, false, ErrPoolEmpty
	}

	// pick a ready test database from the index
	if len(hashDBs.dbs) <= index {
		return TestDatabase{}, false, ErrInvalidIndex
	}

	return hashDBs.dbs[index], isDirty, nil
	// DBPool unlocked
	// !
}

func (p *DBPool) AddTestDatabase(ctx context.Context, template TemplateConfig, dbNamePrefix string, initFunc func(TestDatabase) error) (TestDatabase, error) {
	var newTestDB TestDatabase
	hash := template.TemplateHash

	p.Lock()
	defer p.Unlock()

	hashDBs := p.pool[hash]
	if hashDBs == nil {
		hashDBs = newDBHashPool(p.maxPoolSize)
		p.pool[hash] = hashDBs
	}

	// get index of a next test DB - its ID
	index := len(hashDBs.dbs)
	if index >= p.maxPoolSize {
		return TestDatabase{}, ErrPoolFull
	}

	{
		// initalization of a new DB
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

		if err := initFunc(newTestDB); err != nil {
			return TestDatabase{}, err
		}
	}

	// add new test DB to the pool
	hashDBs.dbs = append(hashDBs.dbs, newTestDB)

	// and add its index to 'ready'
	ready := p.ready[hash]
	if ready == nil {
		ready = make(dbIDMap)
	}

	ready[index] = true
	p.ready[hash] = ready

	return newTestDB, nil
	// DBPool unlocked
	// !
}

func (p *DBPool) ReturnTestDatabase(ctx context.Context, hash string, id int) error {

	// !
	// DBPool locked
	p.Lock()
	defer p.Unlock()

	if id < 0 || id >= p.maxPoolSize {
		return ErrInvalidIndex
	}

	dirty := p.dirty[hash]
	// check if pool has been already returned
	if dirty != nil && len(dirty) > 0 {
		exists := dirty[id]
		if exists {
			return ErrNotInPool
		}
	}

	// ok, it hasn't been returned yet
	dirty[id] = true
	p.dirty[hash] = dirty

	return nil
}
