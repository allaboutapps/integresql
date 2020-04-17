package pgtestpool

import (
	"context"
	"sync"
)

type Database struct {
	sync.RWMutex `json:"-"`

	TemplateHash string         `json:"templateHash"`
	Config       DatabaseConfig `json:"config"`

	ready bool
	c     chan struct{}
}

func (d *Database) Ready() bool {
	d.RLock()
	defer d.RUnlock()

	return d.ready
}

func (d *Database) WaitUntilReady(ctx context.Context) error {
	if d.Ready() {
		return nil
	}

	for {
		select {
		case <-d.c:
			if d.Ready() {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (d *Database) FlagAsReady() {
	if d.Ready() {
		return
	}

	d.Lock()
	defer d.Unlock()

	d.ready = true

	if d.c != nil {
		close(d.c)
	}
}
