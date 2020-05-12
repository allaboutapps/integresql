package manager

import (
	"context"
	"errors"
	"sync"
)

type databaseState int

const (
	databaseStateInit      databaseState = iota
	databaseStateDiscarded databaseState = iota
	databaseStateReady     databaseState = iota
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

	return d.state == databaseStateReady
}

func (d *Database) WaitUntilReady(ctx context.Context) error {

	state := d.State()

	if state == databaseStateReady {
		return nil
	} else if state == databaseStateDiscarded {
		return ErrDatabaseDiscarded
	}

	for {
		select {
		case <-d.c:
			state := d.State()

			if state == databaseStateReady {
				return nil
			} else if state == databaseStateDiscarded {
				return ErrDatabaseDiscarded
			}

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (d *Database) FlagAsReady() {

	state := d.State()
	if state != databaseStateInit {
		return
	}

	d.Lock()
	defer d.Unlock()

	d.state = databaseStateReady

	if d.c != nil {
		close(d.c)
	}
}

func (d *Database) FlagAsDiscarded() {

	state := d.State()
	if state != databaseStateInit {
		return
	}

	d.Lock()
	defer d.Unlock()

	d.state = databaseStateDiscarded

	if d.c != nil {
		close(d.c)
	}
}
