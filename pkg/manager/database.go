package manager

import (
	"context"
	"errors"
	"runtime/trace"
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

func (d *Database) State(ctx context.Context) databaseState {
	reg := trace.StartRegion(ctx, "db_get_state")
	defer reg.End()

	d.RLock()
	defer d.RUnlock()

	return d.state
}

func (d *Database) Ready(ctx context.Context) bool {
	reg := trace.StartRegion(ctx, "db_check_ready")
	defer reg.End()

	d.RLock()
	defer d.RUnlock()

	return d.state == databaseStateReady
}

func (d *Database) WaitUntilReady(ctx context.Context) error {
	reg := trace.StartRegion(ctx, "db_wait_ready")
	defer reg.End()

	state := d.State(ctx)

	if state == databaseStateReady {
		return nil
	} else if state == databaseStateDiscarded {
		return ErrDatabaseDiscarded
	}

	for {
		select {
		case <-d.c:
			state := d.State(ctx)

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

func (d *Database) FlagAsReady(ctx context.Context) {
	reg := trace.StartRegion(ctx, "db_flag_ready")
	defer reg.End()

	state := d.State(ctx)
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

func (d *Database) FlagAsDiscarded(ctx context.Context) {
	reg := trace.StartRegion(ctx, "db_flag_discarded")
	defer reg.End()

	state := d.State(ctx)
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
