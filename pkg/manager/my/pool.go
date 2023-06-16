package manager

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrPoolEmpty = errors.New("no database exists for this hash")
	ErrPoolFull  = errors.New("database pool is full")
)

type DBPool struct {
	ready map[string]*singleHashDBPool // map[hash]
	dirty map[string]*singleHashDBPool // map[hash][db ID]
	sync.RWMutex

	poolSize int
}

type singleHashDBPool struct {
	dbs   []TestDatabase
	index int

	sync.RWMutex
}

func newSingleHashDBPool(poolSize int) *singleHashDBPool {
	return &singleHashDBPool{
		dbs:   make([]TestDatabase, poolSize),
		index: -1,
	}
}

func (p *DBPool) GetReadyDB(ctx context.Context, hash string) (TestDatabase, error) {
	var readyDBs *singleHashDBPool
	{
		//
		// DBPool locked
		p.Lock()
		defer p.Unlock()
		readyDBs = p.ready[hash]
		if readyDBs == nil {
			return TestDatabase{}, ErrPoolEmpty
		}
	}

	//
	// singleHashDBPool locked
	//
	readyDBs.Lock()
	defer readyDBs.Unlock()

	// if index is negative, there are no ready DBs
	if readyDBs.index < 0 {
		return TestDatabase{}, ErrPoolEmpty
	}

	// pick a test database from the index
	// and decrease the index value - this database is now 'dirty'
	testDB := readyDBs.dbs[readyDBs.index]
	readyDBs.index--

	// add it to the collection of dirty DB

	return testDB, nil
}

func (p *DBPool) AddTestDatabase(ctx context.Context, template TemplateConfig, dbNamePrefix string) (TestDatabase, error) {
	var readyDBs *singleHashDBPool
	{
		//
		// DBPool locked
		p.Lock()
		defer p.Unlock()

		readyDBs, ok := p.ready[template.TemplateHash]
		if ok {
			// if exists already, check if pool size is not exceeded
			if readyDBs.index+1 >= p.poolSize {
				return TestDatabase{}, ErrPoolFull
			}

		} else {
			// add newSingleHashDBPool if doesn't exist already
			readyDBs = newSingleHashDBPool(p.poolSize)
			p.ready[template.TemplateHash] = readyDBs
		}
	}

	//
	// singleHashDBPool locked
	//
	readyDBs.Lock()
	defer readyDBs.Unlock()

	// index points now to the DB to be added
	readyDBs.index++

	// prepare test database structure based on the template
	newTestDB := TestDatabase{
		Database: Database{
			TemplateHash: template.TemplateHash,
			Config:       template.Config,
		},
		ID: readyDBs.index,
	}
	dbName := fmt.Sprintf("%s%03d", dbNamePrefix, readyDBs.index)
	newTestDB.Database.Config.Database = dbName

	// add new database to ready pool
	readyDBs.dbs[readyDBs.index] = newTestDB

	return newTestDB, nil
}
