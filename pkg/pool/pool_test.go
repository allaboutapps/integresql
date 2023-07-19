package pool_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/allaboutapps/integresql/pkg/db"
	"github.com/allaboutapps/integresql/pkg/pool"
	"github.com/stretchr/testify/assert"
)

func TestPoolAddGet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := pool.PoolConfig{
		MaxPoolSize:      2,
		NumOfWorkers:     4,
		TestDBNamePrefix: "prefix_",
		ForceDBReturn:    true,
	}
	p := pool.NewDBPool(cfg)

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

	// get from empty
	_, err := p.GetTestDatabase(ctx, hash1, 0)
	assert.Error(t, err, pool.ErrTimeout)

	// add a new one
	assert.NoError(t, p.AddTestDatabase(ctx, templateDB, initFunc))
	// get it
	testDB, err := p.GetTestDatabase(ctx, hash1, 0)
	assert.NoError(t, err)
	assert.Equal(t, "prefix_h1_000", testDB.Database.Config.Database)
	assert.Equal(t, "ich", testDB.Database.Config.Username)

	// add for h2
	templateDB.TemplateHash = hash2
	assert.NoError(t, p.AddTestDatabase(ctx, templateDB, initFunc))
	assert.NoError(t, p.AddTestDatabase(ctx, templateDB, initFunc))
	assert.ErrorIs(t, p.AddTestDatabase(ctx, templateDB, initFunc), pool.ErrPoolFull)

	// get from empty h1
	_, err = p.GetTestDatabase(ctx, hash1, 0)
	assert.Error(t, err, pool.ErrTimeout)

	// get from h2
	testDB1, err := p.GetTestDatabase(ctx, hash2, 0)
	assert.NoError(t, err)
	assert.Equal(t, hash2, testDB1.TemplateHash)
	testDB2, err := p.GetTestDatabase(ctx, hash2, 0)
	assert.NoError(t, err)
	assert.Equal(t, hash2, testDB2.TemplateHash)
	assert.NotEqual(t, testDB1.ID, testDB2.ID)

	p.Stop()
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

	cfg := pool.PoolConfig{
		MaxPoolSize:      6,
		NumOfWorkers:     4,
		TestDBNamePrefix: "",
		ForceDBReturn:    true,
	}
	p := pool.NewDBPool(cfg)

	var wg sync.WaitGroup
	sleepDuration := 100 * time.Millisecond

	// initialize hash pool
	p.InitHashPool(ctx, templateDB1, initFunc)
	p.InitHashPool(ctx, templateDB2, initFunc)

	// add DB in one goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()

		templateDB1 := templateDB1
		templateDB2 := templateDB2
		sleepDuration := sleepDuration

		// add DBs sequentially
		for i := 0; i < cfg.MaxPoolSize; i++ {
			assert.NoError(t, p.AddTestDatabase(ctx, templateDB1, initFunc))
			assert.NoError(t, p.AddTestDatabase(ctx, templateDB2, initFunc))
			time.Sleep(sleepDuration)
		}
	}()

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
	p.Stop()
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
		t.Log("(re)create ", testDB.Database)
		return nil
	}

	cfg := pool.PoolConfig{
		MaxPoolSize:      6,
		NumOfWorkers:     4,
		TestDBNamePrefix: "",
		ForceDBReturn:    true,
	}
	p := pool.NewDBPool(cfg)

	var wg sync.WaitGroup

	// add DBs sequentially
	for i := 0; i < cfg.MaxPoolSize/2; i++ {
		assert.NoError(t, p.AddTestDatabase(ctx, templateDB1, initFunc))
		assert.NoError(t, p.AddTestDatabase(ctx, templateDB2, initFunc))
	}

	// try to get them from another goroutines in parallel
	getAndReturnDB := func(hash string) {
		defer wg.Done()

		db, err := p.GetTestDatabase(ctx, hash, 3*time.Second)
		assert.NoError(t, err)
		assert.Equal(t, hash, db.TemplateHash)
		t.Logf("returning %s %v\n", db.TemplateHash, db.ID)
		assert.NoError(t, p.ReturnTestDatabase(ctx, hash, db.ID))
	}

	for i := 0; i < cfg.MaxPoolSize*3; i++ {
		wg.Add(2)
		go getAndReturnDB(hash1)
		go getAndReturnDB(hash2)
	}

	wg.Wait()
	p.Stop()
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
	removeFunc := func(testDB db.TestDatabase) error {
		t.Log("remove ", testDB.Database)
		return nil
	}

	cfg := pool.PoolConfig{
		MaxPoolSize:      6,
		NumOfWorkers:     4,
		TestDBNamePrefix: "",
		ForceDBReturn:    true,
	}
	p := pool.NewDBPool(cfg)

	// add DBs sequentially
	for i := 0; i < cfg.MaxPoolSize; i++ {
		assert.NoError(t, p.AddTestDatabase(ctx, templateDB1, initFunc))
		assert.NoError(t, p.AddTestDatabase(ctx, templateDB2, initFunc))
	}

	// remove all
	assert.NoError(t, p.RemoveAll(ctx, removeFunc))

	// try to get
	_, err := p.GetTestDatabase(ctx, hash1, 0)
	assert.Error(t, err, pool.ErrTimeout)
	_, err = p.GetTestDatabase(ctx, hash2, 0)
	assert.Error(t, err, pool.ErrTimeout)

	// start using pool again
	assert.NoError(t, p.AddTestDatabase(ctx, templateDB1, initFunc))
	testDB, err := p.GetTestDatabase(ctx, hash1, 0)
	assert.NoError(t, err)
	assert.Equal(t, 0, testDB.ID)

	p.Stop()
}

func TestPoolInit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	hash1 := "h1"
	templateDB1 := db.Database{
		TemplateHash: hash1,
	}

	initFunc := func(ctx context.Context, testDB db.TestDatabase, templateName string) error {
		t.Log("(re)create ", testDB.Database)
		return nil
	}

	cfg := pool.PoolConfig{
		MaxPoolSize:      100,
		NumOfWorkers:     150,
		TestDBNamePrefix: "",
		ForceDBReturn:    true,
	}
	p := pool.NewDBPool(cfg)

	// we will test 2 ways of adding new DBs
	for i := 0; i < cfg.MaxPoolSize/2; i++ {
		// add and get freshly added DB
		assert.NoError(t, p.AddTestDatabase(ctx, templateDB1, initFunc))
		_, err := p.GetTestDatabase(ctx, templateDB1.TemplateHash, time.Millisecond)
		assert.NoError(t, err)

		// extend pool (= add and get)
		_, err = p.ExtendPool(ctx, templateDB1)
		assert.NoError(t, err)
	}

	// there should be no more free DBs
	_, err := p.GetTestDatabase(ctx, templateDB1.TemplateHash, 10*time.Millisecond)
	assert.ErrorIs(t, err, pool.ErrTimeout)

	var wg sync.WaitGroup
	// now return them all
	wg.Add(1)
	go func() {
		defer wg.Done()
		maxPoolSize := cfg.MaxPoolSize
		templateHash := templateDB1.TemplateHash
		for i := 0; i < maxPoolSize; i++ {
			assert.NoError(t, p.ReturnTestDatabase(ctx, templateHash, i))
		}
	}()

	// and check that they can be get again
	// = the workers cleaned them up
	wg.Add(1)
	go func() {
		defer wg.Done()
		maxPoolSize := cfg.MaxPoolSize
		templateHash := templateDB1.TemplateHash
		for i := 0; i < maxPoolSize; i++ {
			_, err := p.GetTestDatabase(ctx, templateHash, 10*time.Millisecond)
			assert.NoError(t, err)
		}
	}()

	wg.Wait()

	p.Stop()
}

func TestPoolExtendRecyclingInUseTestDB(t *testing.T) {
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

	cfg := pool.PoolConfig{
		MaxPoolSize:      40,
		NumOfWorkers:     1,
		TestDBNamePrefix: "test_",
		ForceDBReturn:    false,
	}
	p := pool.NewDBPool(cfg)
	p.InitHashPool(ctx, templateDB1, initFunc)

	for i := 0; i < cfg.MaxPoolSize; i++ {
		// add and get freshly added DB
		// LEGACY HANDLING not supported!
		_, err := p.ExtendPool(ctx, templateDB1)
		assert.Error(t, err)
	}

	forceExtend := func(seenIDMap *sync.Map) {
		newTestDB1, err := p.ExtendPool(ctx, templateDB1)
		assert.Error(t, err)
		seenIDMap.Store(newTestDB1.ID, true)
	}

	// allow for recycling inUse test DBs
	var wg sync.WaitGroup
	seenIDMap := sync.Map{}
	for i := 0; i < 3*cfg.MaxPoolSize; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			forceExtend(&seenIDMap)
		}()
	}

	wg.Wait()

	// NOPE, not supported!
	// for id := 0; id < cfg.MaxPoolSize; id++ {
	// 	_, ok := seenIDMap.Load(id)
	// 	// every index that %5 != 0 should show up at least once
	// 	assert.True(t, ok, id)
	// }

	p.Stop()
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
		MaxPoolSize:   40,
		NumOfWorkers:  3,
		ForceDBReturn: true,
	}
	p := pool.NewDBPool(cfg)
	p.InitHashPool(ctx, templateDB1, initFunc)

	for i := 0; i < cfg.MaxPoolSize; i++ {
		testDB, err := p.ExtendPool(ctx, templateDB1)
		assert.NoError(t, err)
		// return - don't recreate, just bring back directly to the pool
		assert.NoError(t, p.ReturnTestDatabase(ctx, hash1, testDB.ID))
	}

	for id := 0; id < cfg.MaxPoolSize; id++ {
		recreatedTimes, ok := recreateTimesMap.Load(id)
		assert.True(t, ok)
		assert.Equal(t, 1, recreatedTimes) // just once to initialize it
	}

	p.Stop()
}

func TestPoolRestoreTestDatabase(t *testing.T) {
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
		MaxPoolSize:   40,
		NumOfWorkers:  3,
		ForceDBReturn: true,
	}
	p := pool.NewDBPool(cfg)
	p.InitHashPool(ctx, templateDB1, initFunc)

	for i := 0; i < cfg.MaxPoolSize; i++ {
		testDB, err := p.ExtendPool(ctx, templateDB1)
		assert.NoError(t, err)
		// restore - add for cleaning
		assert.NoError(t, p.RestoreTestDatabase(ctx, hash1, testDB.ID))
	}

	time.Sleep(100 * time.Millisecond) // wait a tiny bit to have all DB cleaned up

	for id := 0; id < cfg.MaxPoolSize; id++ {
		recreatedTimes, ok := recreateTimesMap.Load(id)
		assert.True(t, ok)
		assert.Equal(t, 2, recreatedTimes) // first time to initialize it, second to clean it
	}

	p.Stop()
}
