package templates

import (
	"context"
	"runtime/trace"
	"sync"

	"github.com/allaboutapps/integresql/pkg/db"
)

type Collection struct {
	templates map[string]*Template
	collMutex sync.RWMutex
}

type Unlock func()

func NewCollection() *Collection {
	return &Collection{
		templates: make(map[string]*Template),
		collMutex: sync.RWMutex{},
	}
}

func (tc *Collection) Push(ctx context.Context, hash string, template db.DatabaseConfig) (added bool, unlock Unlock) {
	reg := trace.StartRegion(ctx, "get_template_lock")
	tc.collMutex.Lock()

	unlock = func() {
		tc.collMutex.Unlock()
		reg.End()
	}

	_, ok := tc.templates[hash]
	if ok {
		return false, unlock
	}

	tc.templates[hash] = NewTemplate(db.Database{TemplateHash: hash, Config: template})
	return true, unlock
}

func (tc *Collection) Pop(ctx context.Context, hash string) (template *Template, found bool) {
	reg := trace.StartRegion(ctx, "get_template_lock")
	defer reg.End()
	tc.collMutex.Lock()
	defer tc.collMutex.Unlock()

	template, ok := tc.templates[hash]
	if !ok {
		return nil, false
	}

	delete(tc.templates, hash)
	return template, true
}

func (tc *Collection) Get(ctx context.Context, hash string) (template *Template, found bool) {
	reg := trace.StartRegion(ctx, "get_template_lock")
	defer reg.End()

	tc.collMutex.RLock()
	defer tc.collMutex.RUnlock()

	template, ok := tc.templates[hash]
	if !ok {
		return nil, false
	}

	return template, true
}

func (tc *Collection) RemoveUnsafe(ctx context.Context, hash string) {
	delete(tc.templates, hash)
}

func (tc *Collection) RemoveAll(ctx context.Context) {
	reg := trace.StartRegion(ctx, "get_template_lock")
	defer reg.End()

	tc.collMutex.Lock()
	defer tc.collMutex.Unlock()

	for hash, template := range tc.templates {
		template.SetState(ctx, TemplateStateDiscarded)

		delete(tc.templates, hash)
	}
}
