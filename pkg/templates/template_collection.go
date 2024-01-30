package templates

import (
	"context"
	"runtime/trace"
	"sync"
)

type Collection struct {
	templates map[string]*Template
	collMutex sync.RWMutex
}

// Unlock function used to release the collection lock.
type Unlock func()

func NewCollection() *Collection {
	return &Collection{
		templates: make(map[string]*Template),
		collMutex: sync.RWMutex{},
	}
}

// Push tries to add a new template to the collection.
// If the template already exists and the config matches, added=false is returned.
// If config doesn't match, the template is overwritten and added=true is returned.
// This function locks the collection and no matter what is its output, the unlock function needs to be called to release the lock.
func (tc *Collection) Push(ctx context.Context, hash string, config TemplateConfig) (added bool, unlock Unlock) {
	reg := trace.StartRegion(ctx, "get_template_lock")
	tc.collMutex.Lock()

	unlock = func() {
		tc.collMutex.Unlock()
		reg.End()
	}

	template, ok := tc.templates[hash]
	if ok {
		// check if settings match

		if template.GetConfig(ctx).Equals(config) {
			return false, unlock
		}
		// else overwrite the template
	}

	tc.templates[hash] = NewTemplate(hash, config)
	return true, unlock
}

// Pop removes a template from the collection returning it to the caller.
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

// Get gets the requested template without removing it from the collection.
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

// RemoveUnsafe removes the template and can be called ONLY IF THE COLLECTION IS LOCKED.
func (tc *Collection) RemoveUnsafe(_ context.Context, hash string) {
	delete(tc.templates, hash)
}

// RemoveAll removes all templates from the collection.
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
