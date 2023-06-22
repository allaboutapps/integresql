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
	p := pool.NewDBPool(2)

	hash1 := "h1"
	hash2 := "h2"
	templateDB := db.Database{
		TemplateHash: hash1,
		Config: db.DatabaseConfig{
			Username: "ich",
			Database: "template_name",
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
	assert.Equal(t, "template_name_000", testDB.Database.Config.Database)
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
	p := pool.NewDBPool(maxPoolSize)

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
	p := pool.NewDBPool(maxPoolSize)

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
	p := pool.NewDBPool(maxPoolSize)

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
