package templates

import (
	"context"
	"sync"
	"time"

	"github.com/allaboutapps/integresql/pkg/db"
)

type TemplateState int32

const (
	TemplateStateInit TemplateState = iota
	TemplateStateDiscarded
	TemplateStateReady
)

type Template struct {
	db.Database
	state TemplateState

	cond  *sync.Cond
	mutex sync.RWMutex
}

func NewTemplate(database db.Database) *Template {
	t := &Template{
		Database: database,
		state:    TemplateStateInit,
	}
	t.cond = sync.NewCond(&t.mutex)

	return t
}

func (t *Template) GetState(ctx context.Context) TemplateState {
	t.mutex.RLock()
	defer t.mutex.RUnlock()

	return t.state
}

func (t *Template) SetState(ctx context.Context, newState TemplateState) {
	if t.GetState(ctx) == newState {
		return
	}

	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.state = newState

	t.cond.Broadcast()
}

func (t *Template) WaitUntilReady(ctx context.Context, timeout time.Duration) (exitState TemplateState) {
	currentState := t.GetState(ctx)
	if currentState == TemplateStateReady {
		return
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	newStateChan := make(chan TemplateState, 1)
	go func() {
		t.cond.L.Lock()
		defer t.cond.L.Unlock()
		t.cond.Wait()

		newStateChan <- t.state
	}()

	select {
	case state := <-newStateChan:
		return state
	case <-cctx.Done():
		// timeout means that there were no state changes in the meantime
		return currentState
	}
}
