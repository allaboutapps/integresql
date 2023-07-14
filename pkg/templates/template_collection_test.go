package templates_test

import (
	"context"
	"testing"
	"time"

	"github.com/allaboutapps/integresql/pkg/db"
	"github.com/allaboutapps/integresql/pkg/templates"
	"github.com/allaboutapps/integresql/pkg/util"
	"github.com/stretchr/testify/assert"
)

func TestTemplateCollection(t *testing.T) {
	ctx := context.Background()

	coll := templates.NewCollection()
	cfg := templates.TemplateConfig{
		DatabaseConfig: db.DatabaseConfig{
			Username: "ich",
			Database: "template_test",
		},
	}
	hash := "123"

	added, unlock := coll.Push(ctx, hash, cfg)
	assert.True(t, added)
	unlock()

	template1, found := coll.Get(ctx, hash)
	assert.True(t, found)

	// get with lock
	state, lockedTemplate := template1.GetStateWithLock(ctx)
	assert.Equal(t, templates.TemplateStateInit, state)

	// try to get again when the template is locked
	template2, found := coll.Get(ctx, hash)
	assert.True(t, found)

	// assert that getting the state now won't succeed - template is locked
	_, err := util.WaitWithTimeout(ctx, 100*time.Millisecond, func(ctx context.Context) (templates.TemplateState, error) {
		return template1.GetState(ctx), nil
	})
	assert.ErrorIs(t, err, util.ErrTimeout)
	_, err = util.WaitWithTimeout(ctx, 100*time.Millisecond, func(ctx context.Context) (templates.TemplateState, error) {
		return template2.GetState(ctx), nil
	})
	assert.ErrorIs(t, err, util.ErrTimeout)

	// now set the new state and unlock the locked template
	lockedTemplate.SetState(ctx, templates.TemplateStateDiscarded)
	lockedTemplate.Unlock()
	lockedTemplate.Unlock()

	assert.Equal(t, templates.TemplateStateDiscarded, template2.GetState(ctx))

	// make sure that the template is still in the collection
	template3, found := coll.Get(ctx, hash)
	assert.True(t, found)
	assert.Equal(t, "ich", template3.Config.Username)
}

func TestTemplateCollectionPushWithOtherConfig(t *testing.T) {
	ctx := context.Background()

	coll := templates.NewCollection()
	cfg := templates.TemplateConfig{
		DatabaseConfig: db.DatabaseConfig{
			Username: "ich",
			Database: "template_test",
		},
		RecreateEnabled: true,
	}
	hash := "123"

	added, unlock := coll.Push(ctx, hash, cfg)
	assert.True(t, added)
	unlock()

	added, unlock = coll.Push(ctx, hash, cfg)
	assert.False(t, added)
	unlock()

	cfg.RecreateEnabled = false
	cfg.Database = "template_another"
	added, unlock = coll.Push(ctx, hash, cfg)
	assert.True(t, added)
	unlock()

	// try to get again when the template is locked
	template, found := coll.Get(ctx, hash)
	assert.True(t, found)
	assert.False(t, template.RecreateEnabled)
	assert.Equal(t, "template_another", template.Config.Database)

}
