package pool_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/allaboutapps/integresql/pkg/db"
	"github.com/allaboutapps/integresql/pkg/pool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPoolAddGet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := pool.PoolConfig{
		MaxPoolSize:            2,
		PoolMaxParallelTasks:   4,
		TestDBNamePrefix:       "prefix_",
		DisableWorkerAutostart: true, // no extend / cleanDirty tasks should run automatically!
	}
	p := pool.NewPoolCollection(cfg)

	hash1 := "h1"
	hash2 := "h2"
	templateDB := db.Database{
		TemplateHash: hash1,
		Config: db.DatabaseConfig{
			Username: "ich",
			Database: "templateDBname",
		},
	}
	initFunc := func(ctx context.Context, testDB db.TestDatabase, templateName string) error {
		t.Log("(re)create ", testDB.Database)
		return nil
	}
	p.InitHashPool(ctx, templateDB, initFunc)

	t.Cleanup(func() { p.Stop() })

	// get from empty (just initialized)
	_, err := p.GetTestDatabase(ctx, hash1, 0)
	assert.Error(t, err, pool.ErrTimeout)

	// add a new one
	assert.NoError(t, p.AddTestDatabase(ctx, templateDB))
	// get it
	testDB, err := p.GetTestDatabase(ctx, hash1, 1*time.Second)
	assert.NoError(t, err)
	assert.Equal(t, "prefix_h1_000", testDB.Database.Config.Database)
	assert.Equal(t, "ich", testDB.Database.Config.Username)

	// add for h2
	templateDB2 := templateDB
	templateDB2.TemplateHash = hash2
	p.InitHashPool(ctx, templateDB2, initFunc)
	assert.NoError(t, p.AddTestDatabase(ctx, templateDB2))
	assert.NoError(t, p.AddTestDatabase(ctx, templateDB2))
	assert.ErrorIs(t, p.AddTestDatabase(ctx, templateDB2), pool.ErrPoolFull)

	// get from empty h1
	_, err = p.GetTestDatabase(ctx, hash1, 100*time.Millisecond)
	assert.ErrorIs(t, err, pool.ErrTimeout)

	// get from h2
	testDB1, err := p.GetTestDatabase(ctx, hash2, 1*time.Second)
	assert.NoError(t, err)
	assert.Equal(t, hash2, testDB1.TemplateHash)
	testDB2, err := p.GetTestDatabase(ctx, hash2, 1*time.Second)
	assert.NoError(t, err)
	assert.Equal(t, hash2, testDB2.TemplateHash)
	assert.NotEqual(t, testDB1.ID, testDB2.ID)
}

func TestPoolAddGetConcurrent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	hash1 := "h1"
	hash2 := "h2"
	templateDB1 := db.Database{
		TemplateHash: hash1,
	}
	templateDB2 := db.Database{
		TemplateHash: hash2,
	}
	initFunc := func(ctx context.Context, testDB db.TestDatabase, templateName string) error {
		t.Log("(re)create ", testDB.Database)
		return nil
	}

	maxPoolSize := 15
	cfg := pool.PoolConfig{
		MaxPoolSize:          maxPoolSize,
		InitialPoolSize:      maxPoolSize,
		PoolMaxParallelTasks: 4,
		TestDBNamePrefix:     "",
	}
	p := pool.NewPoolCollection(cfg)
	t.Cleanup(func() { p.Stop() })

	var wg sync.WaitGroup
	sleepDuration := 10 * time.Millisecond

	// initialize hash pool
	// initial test databases will be added automatically
	p.InitHashPool(ctx, templateDB1, initFunc)
	p.InitHashPool(ctx, templateDB2, initFunc)

	// try to get them from another goroutines in parallel
	getDB := func(hash string) {
		defer wg.Done()

		sleepDuration := sleepDuration

		db, err := p.GetTestDatabase(ctx, hash, time.Duration(cfg.MaxPoolSize)*sleepDuration)
		assert.NoError(t, err)
		assert.Equal(t, hash, db.TemplateHash)
		t.Logf("got %s %v\n", db.TemplateHash, db.ID)
	}

	for i := 0; i < cfg.MaxPoolSize; i++ {
		wg.Add(2)
		go getDB(hash1)
		go getDB(hash2)
	}

	wg.Wait()

}

func TestPoolAddGetReturnConcurrent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	hash1 := "h1"
	hash2 := "h2"
	templateDB1 := db.Database{
		TemplateHash: hash1,
	}
	templateDB2 := db.Database{
		TemplateHash: hash2,
	}
	initFunc := func(ctx context.Context, testDB db.TestDatabase, templateName string) error {
		return nil
	}

	cfg := pool.PoolConfig{
		MaxPoolSize:          40,
		PoolMaxParallelTasks: 4,
		TestDBNamePrefix:     "",
	}
	p := pool.NewPoolCollection(cfg)
	t.Cleanup(func() { p.Stop() })

	p.InitHashPool(ctx, templateDB1, initFunc)
	p.InitHashPool(ctx, templateDB2, initFunc)

	var wg sync.WaitGroup

	// add DBs sequentially
	for i := 0; i < cfg.MaxPoolSize/4; i++ {
		assert.NoError(t, p.AddTestDatabase(ctx, templateDB1))
		assert.NoError(t, p.AddTestDatabase(ctx, templateDB2))
	}

	// stop the workers to prevent auto cleaning in background
	p.Stop()

	// try to get them from another goroutines in parallel
	getAndReturnDB := func(hash string) {
		defer wg.Done()

		db, err := p.GetTestDatabase(ctx, hash, 3*time.Second)
		assert.NoError(t, err)
		assert.Equal(t, hash, db.TemplateHash)
		t.Logf("returning %s %v\n", db.TemplateHash, db.ID)
		assert.NoError(t, p.ReturnTestDatabase(ctx, hash, db.ID))
	}

	for i := 0; i < cfg.MaxPoolSize; i++ {
		wg.Add(2)
		go getAndReturnDB(hash1)
		go getAndReturnDB(hash2)
	}

	wg.Wait()
}

func TestPoolRemoveAll(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	hash1 := "h1"
	hash2 := "h2"
	templateDB1 := db.Database{
		TemplateHash: hash1,
	}
	templateDB2 := db.Database{
		TemplateHash: hash2,
	}
	initFunc := func(ctx context.Context, testDB db.TestDatabase, templateName string) error {
		t.Log("(re)create ", testDB.Database)
		return nil
	}
	removeFunc := func(ctx context.Context, testDB db.TestDatabase) error {
		t.Log("remove ", testDB.Database)
		return nil
	}

	cfg := pool.PoolConfig{
		MaxPoolSize:          6,
		PoolMaxParallelTasks: 4,
	}
	p := pool.NewPoolCollection(cfg)
	t.Cleanup(func() { p.Stop() })

	p.InitHashPool(ctx, templateDB1, initFunc)
	p.InitHashPool(ctx, templateDB2, initFunc)

	// add DBs sequentially
	for i := 0; i < cfg.MaxPoolSize; i++ {
		assert.NoError(t, p.AddTestDatabase(ctx, templateDB1))
		assert.NoError(t, p.AddTestDatabase(ctx, templateDB2))
	}

	// remove all
	assert.NoError(t, p.RemoveAll(ctx, removeFunc))

	// try to get
	_, err := p.GetTestDatabase(ctx, hash1, 0)
	assert.Error(t, err, pool.ErrTimeout)
	_, err = p.GetTestDatabase(ctx, hash2, 0)
	assert.Error(t, err, pool.ErrTimeout)

	// start using pool again
	p.InitHashPool(ctx, templateDB1, initFunc)
	assert.NoError(t, p.AddTestDatabase(ctx, templateDB1))
	testDB, err := p.GetTestDatabase(ctx, hash1, 1*time.Second)
	assert.NoError(t, err)
	assert.Equal(t, 0, testDB.ID)
}

func TestPoolReuseDirty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	hash1 := "h1"
	templateDB1 := db.Database{
		TemplateHash: hash1,
		Config: db.DatabaseConfig{
			Database: "h1_template",
		},
	}

	initFunc := func(ctx context.Context, testDB db.TestDatabase, templateName string) error {
		t.Log("(re)create ", testDB.Database.Config.Database)
		return nil
	}

	maxPoolSize := 40
	cfg := pool.PoolConfig{
		MaxPoolSize:          maxPoolSize,
		InitialPoolSize:      maxPoolSize,
		PoolMaxParallelTasks: 1,
		TestDBNamePrefix:     "test_",
	}
	p := pool.NewPoolCollection(cfg)

	p.InitHashPool(ctx, templateDB1, initFunc)
	t.Cleanup(func() { p.Stop() })

	getDirty := func(seenIDMap *sync.Map) {
		newTestDB1, err := p.GetTestDatabase(ctx, templateDB1.TemplateHash, 1*time.Second)
		assert.NoError(t, err)
		seenIDMap.Store(newTestDB1.ID, true)
	}

	// allow for recycling inUse test DBs
	var wg sync.WaitGroup
	seenIDMap := sync.Map{}
	for i := 0; i < 3*cfg.MaxPoolSize; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			getDirty(&seenIDMap)
		}()
	}

	wg.Wait()

	for id := 0; id < cfg.MaxPoolSize; id++ {
		_, ok := seenIDMap.Load(id)
		// every index should show up at least once
		assert.True(t, ok, id)
	}
}

func TestPoolReturnTestDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	hash1 := "h1"
	templateDB1 := db.Database{
		TemplateHash: hash1,
		Config: db.DatabaseConfig{
			Database: "h1_template",
		},
	}

	recreateTimesMap := sync.Map{}
	initFunc := func(ctx context.Context, testDB db.TestDatabase, templateName string) error {
		times, existing := recreateTimesMap.LoadOrStore(testDB.ID, 1)
		if existing {
			recreateTimesMap.Store(testDB.ID, times.(int)+1)
		}

		return nil
	}

	cfg := pool.PoolConfig{
		MaxPoolSize:            10,
		PoolMaxParallelTasks:   3,
		DisableWorkerAutostart: true, // no extend / cleanDirty tasks should run automatically!
	}
	p := pool.NewPoolCollection(cfg)

	p.InitHashPool(ctx, templateDB1, initFunc)
	// add just one test DB
	require.NoError(t, p.AddTestDatabase(ctx, templateDB1))

	testDB1, err := p.GetTestDatabase(ctx, templateDB1.TemplateHash, time.Millisecond)
	assert.NoError(t, err)

	// assert that workers are stopped and no new DB showed up
	_, err = p.GetTestDatabase(ctx, templateDB1.TemplateHash, time.Millisecond)
	assert.ErrorIs(t, err, pool.ErrTimeout)

	// return and get the same one
	assert.NoError(t, p.ReturnTestDatabase(ctx, hash1, testDB1.ID))
	testDB2, err := p.GetTestDatabase(ctx, templateDB1.TemplateHash, time.Millisecond)
	assert.NoError(t, err)
	assert.Equal(t, testDB1.ID, testDB2.ID)

}
