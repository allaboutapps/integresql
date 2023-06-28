package util

import (
	"context"
	"errors"
	"time"

	"golang.org/x/sync/errgroup"
)

var ErrTimeout = errors.New("timeout while waiting for operation to complete")

// WaitWithTimeout waits for the operation to complete of returns the ErrTimeout.
func WaitWithTimeout[T any](ctx context.Context, timeout time.Duration, operation func(context.Context) (T, error)) (T, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resChan := make(chan T, 1)
	g, cctx := errgroup.WithContext(cctx)

	g.Go(func() error {
		res, err := operation(cctx)
		resChan <- res
		return err
	})

	select {
	case res := <-resChan:
		return res, g.Wait()
	case <-time.After(timeout):
		var empty T
		return empty, ErrTimeout
	}
}

// WaitWithCancellableCtx runs the operation tracking the context state.
// If the given context is cancelled, the function returns directly with ErrTimeout.
func WaitWithCancellableCtx[T any](ctx context.Context, operation func(context.Context) (T, error)) (T, error) {

	resChan := make(chan T, 1)
	g, cctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		res, err := operation(cctx)
		resChan <- res
		return err
	})

	select {
	case res := <-resChan:
		return res, g.Wait()
	case <-ctx.Done():
		var empty T
		return empty, ErrTimeout
	}
}
