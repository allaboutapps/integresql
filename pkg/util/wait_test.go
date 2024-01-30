package util_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/allaboutapps/integresql/pkg/util"
	"github.com/stretchr/testify/assert"
)

func TestWaitWithTimeout(t *testing.T) {
	ctx := context.Background()
	type output struct {
		A int
	}

	// operation timeout
	start := time.Now()
	res, err := util.WaitWithTimeout(ctx, time.Millisecond*100, func(ctx context.Context) (output, error) {
		time.Sleep(time.Millisecond * 200)
		return output{A: 1}, nil
	})
	elapsed := time.Since(start)

	assert.ErrorIs(t, err, util.ErrTimeout)
	assert.Empty(t, res)
	assert.Less(t, elapsed, 150*time.Millisecond)

	// operation completed
	start = time.Now()
	res, err = util.WaitWithTimeout(ctx, time.Millisecond*200, func(ctx context.Context) (output, error) {
		time.Sleep(time.Millisecond * 160)
		return output{A: 1}, nil
	})
	elapsed = time.Since(start)

	assert.NoError(t, err)
	assert.Equal(t, 1, res.A)
	assert.Less(t, elapsed, 180*time.Millisecond)

	// operation completed with error
	testErr := errors.New("test error")
	start = time.Now()
	res, err = util.WaitWithTimeout(ctx, time.Millisecond*100, func(ctx context.Context) (output, error) {
		return output{}, testErr
	})
	elapsed = time.Since(start)

	assert.ErrorIs(t, err, testErr)
	assert.Empty(t, res)
	assert.Less(t, elapsed, 120*time.Millisecond)
}
