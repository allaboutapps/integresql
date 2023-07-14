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
	TemplateStateFinalized
)

type Template struct {
	TemplateConfig
	db.Database
	state TemplateState

	cond  *sync.Cond
	mutex sync.RWMutex
}

type TemplateConfig struct {
	db.DatabaseConfig
	ResetEnabled bool
}

func NewTemplate(hash string, config TemplateConfig) *Template {
	t := &Template{
		TemplateConfig: config,
		Database:       db.Database{TemplateHash: hash, Config: config.DatabaseConfig},
		state:          TemplateStateInit,
	}
	t.cond = sync.NewCond(&t.mutex)

	return t
}

func (t *Template) IsResetEnabled(ctx context.Context) bool {
	t.mutex.RLock()
	defer t.mutex.RUnlock()

	return t.ResetEnabled
}

// GetState locks the template and checks its state.
func (t *Template) GetState(ctx context.Context) TemplateState {
	t.mutex.RLock()
	defer t.mutex.RUnlock()

	return t.state
}

// SetState sets the desired state and broadcasts the change to whoever is waiting for it.
func (t *Template) SetState(ctx context.Context, newState TemplateState) {
	if t.GetState(ctx) == newState {
		return
	}

	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.state = newState

	t.cond.Broadcast()
}

// WaitUntilFinalized checks the current template state and returns directly if it's 'Finalized'.
// If it's not, the function waits the given timeout until the template state changes.
// On timeout, the old state is returned, otherwise - the new state.
func (t *Template) WaitUntilFinalized(ctx context.Context, timeout time.Duration) (exitState TemplateState) {
	currentState := t.GetState(ctx)
	if currentState == TemplateStateFinalized {
		return currentState
	}

	newState, err := util.WaitWithTimeout(ctx, timeout, func(context.Context) (TemplateState, error) {
		t.cond.L.Lock()
		defer t.cond.L.Unlock()
		t.cond.Wait()

		return t.state, nil
	})

	if err != nil {
		return currentState
	}
	return newState
}

// GetStateWithLock gets the current state leaving the template locked.
// REMEMBER to unlock it when you no longer need it locked.
func (t *Template) GetStateWithLock(ctx context.Context) (TemplateState, lockedTemplate) {
	t.mutex.Lock()

	return t.state, lockedTemplate{t: t}
}

type lockedTemplate struct {
	t *Template
}

// Unlock releases the locked template.
func (l *lockedTemplate) Unlock() {
	if l.t != nil {
		l.t.mutex.Unlock()
		l.t = nil
	}
}

// SetState sets a new state of the locked template (without acquiring the lock again).
func (l lockedTemplate) SetState(ctx context.Context, newState TemplateState) {
	if l.t.state == newState {
		return
	}

	l.t.state = newState
	l.t.cond.Broadcast()
}
