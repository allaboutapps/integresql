package manager

import "context"

type TestDatabase struct {
	Database `json:"database"`

	ID int `json:"id"`

	dirty bool
}

func (t *TestDatabase) Dirty(ctx context.Context) bool {
	t.RLock()
	defer t.RUnlock()

	return t.dirty
}

func (t *TestDatabase) FlagAsDirty(ctx context.Context) {
	t.Lock()
	defer t.Unlock()

	t.dirty = true
}

func (t *TestDatabase) FlagAsClean(ctx context.Context) {
	t.Lock()
	defer t.Unlock()

	t.dirty = false
}

func (t *TestDatabase) ReadyForTest(ctx context.Context) bool {
	return t.Ready(ctx) && !t.Dirty(ctx)
}

type ByID []*TestDatabase

func (i ByID) Len() int           { return len(i) }
func (a ByID) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByID) Less(i, j int) bool { return a[i].ID < a[j].ID }
