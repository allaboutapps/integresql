package manager

import (
	"context"
	"runtime/trace"
	"sync"
)

type TemplateCollection struct {
	templates     map[string]TemplateConfig
	templateMutex sync.RWMutex
}

type Unlock func()

func NewTemplateCollection() *TemplateCollection {
	return &TemplateCollection{
		templates:     make(map[string]TemplateConfig),
		templateMutex: sync.RWMutex{},
	}
}

func (tc *TemplateCollection) Add(ctx context.Context, hash string, template TemplateConfig) (added bool) {
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

func (tc *TemplateCollection) Pop(ctx context.Context, hash string) TemplateConfig {
	reg := trace.StartRegion(ctx, "get_template_lock")
	defer reg.End()

	tc.templateMutex.Lock()
	defer tc.templateMutex.Unlock()

	template, ok := tc.templates[hash]
	if !ok {
		return TemplateConfig{}
	}

	delete(tc.templates, hash)
	return template
}

func (tc *TemplateCollection) Remove(ctx context.Context, hash string) {
	reg := trace.StartRegion(ctx, "get_template_lock")
	defer reg.End()

	tc.templateMutex.Lock()
	defer tc.templateMutex.Unlock()

	delete(tc.templates, hash)
}

// func (tc *TemplateCollection) Get1(ctx context.Context, hash string) (*TemplateConfig, Unlock) {
// 	reg := trace.StartRegion(ctx, "get_template_lock")
// 	tc.templateMutex.Lock()

// 	unlockFunc := func() {
// 		tc.templateMutex.Unlock()
// 		reg.End()
// 	}

// 	template, ok := tc.templates[hash]
// 	if !ok {
// 		return nil, unlockFunc
// 	}
// 	return template, unlockFunc
// }

// func (tc *TemplateCollection) GetForReading1(ctx context.Context, hash string) (*TemplateConfig, Unlock) {
// 	reg := trace.StartRegion(ctx, "get_template_lock")
// 	tc.templateMutex.RLock()

// 	unlockFunc := func() {
// 		tc.templateMutex.RUnlock()
// 		reg.End()
// 	}

// 	template, ok := tc.templates[hash]
// 	if !ok {
// 		return nil, unlockFunc
// 	}
// 	return template, unlockFunc
// }

// func (tc *TemplateCollection) GetAllForReading1() (map[string]*TemplateConfig, Unlock) {
// 	tc.templateMutex.RLock()

// 	unlockFunc := func() {
// 		tc.templateMutex.RUnlock()
// 	}

// 	return tc.templates, unlockFunc
// }

// func (tc *TemplateCollection) Reset1() {
// 	tc.templateMutex.Lock()
// 	defer tc.templateMutex.Unlock()

// 	tc.templates = map[string]*TemplateConfig{}
// }

// func (tc *TemplateCollection) RemoveUnsafe1(hash string) {
// 	// tc.templateMutex.Lock()
// 	// defer tc.templateMutex.Unlock()

// 	delete(tc.templates, hash)
// }

// func (tc *TemplateCollection) AddUnsafe1(hash string, template *TemplateConfig) {
// 	// tc.templateMutex.Lock()
// 	// defer tc.templateMutex.Unlock()

// 	tc.templates[hash] = template
// }
