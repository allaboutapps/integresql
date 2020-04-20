package util

import (
	"fmt"
	"time"
)

func Retry(attempts int, sleep time.Duration, f func() error) error {
	var err error

	for i := 0; i < attempts; i++ {
		err = f()
		if err == nil {
			return nil
		}

		time.Sleep(sleep)
	}

	return fmt.Errorf("failing after %d attempts, lat error: %v", attempts, err)
}
