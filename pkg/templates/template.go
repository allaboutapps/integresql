package templates

import (
	"context"
	"sync"
	"time"

	"github.com/allaboutapps/integresql/pkg/db"
	"github.com/allaboutapps/integresql/pkg/util"
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

	state, err := util.WaitWithTimeout(ctx, timeout, func(context.Context) (TemplateState, error) {
		t.cond.L.Lock()
		defer t.cond.L.Unlock()
		t.cond.Wait()

		return t.state, nil
	})

	if err != nil {
		return currentState
	}
	return state
}
