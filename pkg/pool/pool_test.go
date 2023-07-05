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
	p := pool.NewDBPool(2, "prefix_", 4)

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

	maxPoolSize := 6
	p := pool.NewDBPool(maxPoolSize, "", 4)

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
		for i := 0; i < maxPoolSize; i++ {
			assert.NoError(t, p.AddTestDatabase(ctx, templateDB1, initFunc))
			assert.NoError(t, p.AddTestDatabase(ctx, templateDB2, initFunc))
			time.Sleep(sleepDuration)
		}
	}()

	// try to get them from another goroutines in parallel
	getDB := func(hash string) {
		defer wg.Done()

		sleepDuration := sleepDuration

		db, err := p.GetTestDatabase(ctx, hash, time.Duration(maxPoolSize)*sleepDuration)
		assert.NoError(t, err)
		assert.Equal(t, hash, db.TemplateHash)
		t.Logf("got %s %v\n", db.TemplateHash, db.ID)
	}

	for i := 0; i < maxPoolSize; i++ {
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

	maxPoolSize := 6
	p := pool.NewDBPool(maxPoolSize, "", 4)

	var wg sync.WaitGroup

	// add DBs sequentially
	for i := 0; i < maxPoolSize/2; i++ {
		assert.NoError(t, p.AddTestDatabase(ctx, templateDB1, initFunc))
		assert.NoError(t, p.AddTestDatabase(ctx, templateDB2, initFunc))
	}

	// try to get them from another goroutines in parallel
	getAndReturnDB := func(hash string) {
		defer wg.Done()

		db, err := p.GetTestDatabase(ctx, hash, 3*time.Second)
		assert.NoError(t, err)
		assert.Equal(t, hash, db.TemplateHash)
		time.Sleep(20 * time.Millisecond)
		t.Logf("returning %s %v\n", db.TemplateHash, db.ID)
		assert.NoError(t, p.ReturnTestDatabase(ctx, hash, db.ID))
	}

	for i := 0; i < maxPoolSize*3; i++ {
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

	maxPoolSize := 6
	p := pool.NewDBPool(maxPoolSize, "", 4)

	// add DBs sequentially
	for i := 0; i < maxPoolSize; i++ {
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

	maxPoolSize := 100
	numOfWorkers := 150
	p := pool.NewDBPool(maxPoolSize, "", numOfWorkers)

	// we will test 2 ways of adding new DBs
	for i := 0; i < maxPoolSize/2; i++ {
		// add and get freshly added DB
		assert.NoError(t, p.AddTestDatabase(ctx, templateDB1, initFunc))
		_, err := p.GetTestDatabase(ctx, templateDB1.TemplateHash, time.Millisecond)
		assert.NoError(t, err)

		// extend pool (= add and get)
		_, err = p.ExtendPool(ctx, templateDB1, false /* recycleNotReturned */)
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
		maxPoolSize := maxPoolSize
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
		maxPoolSize := maxPoolSize
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
		t.Log("(re)create ", testDB.Database, ", template name: ", templateName)
		return nil
	}

	maxPoolSize := 40
	numOfWorkers := 1
	p := pool.NewDBPool(maxPoolSize, "test_", numOfWorkers)
	p.InitHashPool(ctx, templateDB1, initFunc)

	for i := 0; i < maxPoolSize; i++ {
		// add and get freshly added DB
		_, err := p.ExtendPool(ctx, templateDB1, false /* recycleNotReturned */)
		assert.NoError(t, err)
	}

	// extend pool not allowing recycling inUse test DBs
	_, err := p.ExtendPool(ctx, templateDB1, false /* recycleNotReturned */)
	assert.ErrorIs(t, err, pool.ErrPoolFull)

	forceExtend := func(seenIDMap *sync.Map) {
		newTestDB1, err := p.ExtendPool(ctx, templateDB1, true /* recycleNotReturned */)
		assert.NoError(t, err)
		assert.Equal(t, hash1, newTestDB1.TemplateHash)
		seenIDMap.Store(newTestDB1.ID, true)
	}

	// allow for recycling inUse test DBs
	var wg sync.WaitGroup
	seenIDMap := sync.Map{}
	for i := 0; i < 3*maxPoolSize; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			forceExtend(&seenIDMap)
		}()
	}

	wg.Wait()

	for id := 0; id < maxPoolSize; id++ {
		_, ok := seenIDMap.Load(id)
		// every index that %5 != 0 should show up at least once
		assert.True(t, ok, id)
	}

	p.Stop()
}
