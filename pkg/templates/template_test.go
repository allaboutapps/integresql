package templates_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/allaboutapps/integresql/pkg/db"
	"github.com/allaboutapps/integresql/pkg/templates"
	"github.com/stretchr/testify/assert"
)

func TestTemplateGetSetState(t *testing.T) {
	ctx := context.Background()

	t1 := templates.NewTemplate(db.Database{TemplateHash: "123"})
	state := t1.GetState(ctx)
	assert.Equal(t, templates.TemplateStateInit, state)

	t1.SetState(ctx, templates.TemplateStateReady)
	state = t1.GetState(ctx)
	assert.Equal(t, templates.TemplateStateReady, state)

	t1.SetState(ctx, templates.TemplateStateDiscarded)
	state = t1.GetState(ctx)
	assert.Equal(t, templates.TemplateStateDiscarded, state)
}

func TestTemplateWaitForReady(t *testing.T) {
	ctx := context.Background()
	goroutineNum := 10

	// initalize a new template, not ready yet
	t1 := templates.NewTemplate(db.Database{TemplateHash: "123"})
	state := t1.GetState(ctx)
	assert.Equal(t, templates.TemplateStateInit, state)

	var wg sync.WaitGroup
	errsChan := make(chan error, 2*goroutineNum)

	// these goroutines should get ready state after waiting long enough
	for i := 0; i < goroutineNum; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			timeout := 1 * time.Second
			state := t1.WaitUntilReady(ctx, timeout)
			if state != templates.TemplateStateReady {
				errsChan <- errors.New("expected ready, but is not")
			}
		}()
	}

	// these goroutines should run into timeout
	for i := 0; i < goroutineNum; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			timeout := 3 * time.Millisecond
			state := t1.WaitUntilReady(ctx, timeout)
			if state != templates.TemplateStateInit {
				errsChan <- errors.New("expected state init, but is not")
			}
		}()
	}

	// now set state
	time.Sleep(5 * time.Millisecond)
	t1.SetState(ctx, templates.TemplateStateReady)

	wg.Wait()
	close(errsChan)

	if len(errsChan) > 0 {
		for err := range errsChan {
			t.Error(err)
		}
		t.Fail()
	}
}
