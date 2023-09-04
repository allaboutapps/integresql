package templates_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/allaboutapps/integresql/pkg/templates"
	"github.com/stretchr/testify/assert"
)

func TestTemplateGetSetState(t *testing.T) {
	ctx := context.Background()

	t1 := templates.NewTemplate("123", templates.TemplateConfig{})
	state := t1.GetState(ctx)
	assert.Equal(t, templates.TemplateStateInit, state)

	t1.SetState(ctx, templates.TemplateStateFinalized)
	state = t1.GetState(ctx)
	assert.Equal(t, templates.TemplateStateFinalized, state)

	t1.SetState(ctx, templates.TemplateStateDiscarded)
	state = t1.GetState(ctx)
	assert.Equal(t, templates.TemplateStateDiscarded, state)
}

func TestForReady(t *testing.T) {
	ctx := context.Background()
	goroutineNum := 10

	// initalize a new template, not ready yet
	t1 := templates.NewTemplate("123", templates.TemplateConfig{})
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
			state := t1.WaitUntilFinalized(ctx, timeout)
			if state != templates.TemplateStateFinalized {
				errsChan <- fmt.Errorf("expected state %v (finalized), but is %v", templates.TemplateStateFinalized, state)
			}
		}()
	}

	// these goroutines should run into timeout
	for i := 0; i < goroutineNum; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			timeout := 30 * time.Millisecond
			state := t1.WaitUntilFinalized(ctx, timeout)
			if state != templates.TemplateStateInit {
				errsChan <- fmt.Errorf("expected state %v (init), but is %v", templates.TemplateStateInit, state)
			}
		}()
	}

	// now set state
	time.Sleep(50 * time.Millisecond)
	t1.SetState(ctx, templates.TemplateStateFinalized)

	wg.Wait()
	close(errsChan)

	if len(errsChan) > 0 {
		for err := range errsChan {
			t.Error(err)
		}
		t.Fail()
	}
}
