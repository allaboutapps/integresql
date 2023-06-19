package templates

import (
	"context"
	"runtime/trace"
	"sync"

	"github.com/allaboutapps/integresql/pkg/db"
)

type Collection struct {
	templates     map[string]db.Database
	templateMutex sync.RWMutex
}

type Unlock func()

func NewCollection() *Collection {
	return &Collection{
		templates:     make(map[string]db.Database),
		templateMutex: sync.RWMutex{},
	}
}

func (tc *Collection) Push(ctx context.Context, hash string, template db.DatabaseConfig) (added bool, unlock Unlock) {
	reg := trace.StartRegion(ctx, "get_template_lock")
	tc.templateMutex.Lock()

	unlock = func() {
		tc.templateMutex.Unlock()
		reg.End()
	}

	_, ok := tc.templates[hash]
	if ok {
		return false, unlock
	}

	tc.templates[hash] = db.Database{TemplateHash: hash, Config: template}
	return true, unlock
}

func (tc *Collection) Pop(ctx context.Context, hash string) (template db.Database, found bool, unlock Unlock) {
	reg := trace.StartRegion(ctx, "get_template_lock")
	tc.templateMutex.Lock()

	unlock = func() {
		tc.templateMutex.Unlock()
		reg.End()
	}

	template, ok := tc.templates[hash]
	if !ok {
		return db.Database{}, false, unlock
	}

	delete(tc.templates, hash)
	return template, true, unlock
}

func (tc *Collection) Get(ctx context.Context, hash string) (template db.Database, found bool, unlock Unlock) {
	reg := trace.StartRegion(ctx, "get_template_lock")
	tc.templateMutex.Lock()

	unlock = func() {
		tc.templateMutex.Unlock()
		reg.End()
	}

	template, ok := tc.templates[hash]
	if !ok {
		return db.Database{}, false, unlock
	}

	return template, true, unlock
}

func (tc *Collection) RemoveUnsafe(ctx context.Context, hash string) {
	delete(tc.templates, hash)
}
