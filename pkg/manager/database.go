package manager

import (
	"context"
	"errors"
	"sync"
)

type databaseState int

const (
	DATABASE_STATE_INIT      databaseState = iota
	DATABASE_STATE_DISCARDED databaseState = iota
	DATABASE_STATE_READY     databaseState = iota
)

var ErrDatabaseDiscarded = errors.New("ErrDatabaseDiscarded")

type Database struct {
	sync.RWMutex `json:"-"`

	TemplateHash string         `json:"templateHash"`
	Config       DatabaseConfig `json:"config"`

	state databaseState
	c     chan struct{}
}

func (d *Database) State() databaseState {
	d.RLock()
	defer d.RUnlock()

	return d.state
}

func (d *Database) Ready() bool {
	d.RLock()
	defer d.RUnlock()

	return d.state == DATABASE_STATE_READY
}

func (d *Database) WaitUntilReady(ctx context.Context) error {

	state := d.State()

	if state == DATABASE_STATE_READY {
		return nil
	} else if state == DATABASE_STATE_DISCARDED {
		return ErrDatabaseDiscarded
	}

	for {
		select {
		case <-d.c:
			state := d.State()

			if state == DATABASE_STATE_READY {
				return nil
			} else if state == DATABASE_STATE_DISCARDED {
				return ErrDatabaseDiscarded
			}

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (d *Database) FlagAsReady() {

	state := d.State()
	if state != DATABASE_STATE_INIT {
		return
	}

	d.Lock()
	defer d.Unlock()

	d.state = DATABASE_STATE_READY

	if d.c != nil {
		close(d.c)
	}
}

func (d *Database) FlagAsDiscarded() {

	state := d.State()
	if state != DATABASE_STATE_INIT {
		return
	}

	d.Lock()
	defer d.Unlock()

	d.state = DATABASE_STATE_DISCARDED

	if d.c != nil {
		close(d.c)
	}
}
