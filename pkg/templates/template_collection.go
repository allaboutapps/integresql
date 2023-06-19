package templates

import (
	"context"
	"runtime/trace"
	"sync"

	"github.com/allaboutapps/integresql/pkg/db"
)

type Collection struct {
	templates     map[string]db.DatabaseConfig
	templateMutex sync.RWMutex
}

func NewCollection() *Collection {
	return &Collection{
		templates:     make(map[string]db.DatabaseConfig),
		templateMutex: sync.RWMutex{},
	}
}

func (tc *Collection) Push(ctx context.Context, hash string, template db.DatabaseConfig) (added bool) {
	reg := trace.StartRegion(ctx, "get_template_lock")
	defer reg.End()

	tc.templateMutex.Lock()
	defer tc.templateMutex.Unlock()

	_, ok := tc.templates[hash]
	if ok {
		return false
	}

	tc.templates[hash] = template
	return true
}

func (tc *Collection) Pop(ctx context.Context, hash string) db.DatabaseConfig {
	reg := trace.StartRegion(ctx, "get_template_lock")
	defer reg.End()

	tc.templateMutex.Lock()
	defer tc.templateMutex.Unlock()

	template, ok := tc.templates[hash]
	if !ok {
		return db.DatabaseConfig{}
	}

	delete(tc.templates, hash)
	return template
}
