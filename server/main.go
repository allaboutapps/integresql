package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/allaboutapps/integresql/server/api"
	"github.com/allaboutapps/integresql/server/router"
	"github.com/allaboutapps/integresql/pgtestpool"
)

func main() {
	manager := pgtestpool.DefaultManagerFromEnv()

	pgtestpoolInitialize := func() error {
		return manager.Initialize(context.Background())
	}

	if err := retry(30, 1*time.Second, pgtestpoolInitialize); err != nil {
		log.Fatalf("Failed to initialize testpool manager: %v", err)
	}

	server := &api.Server{
		M:      manager,
		Config: api.DefaultServerConfigFromEnv(),
	}
	router := router.Init(server)

	go func() {
		if err := router.Start(fmt.Sprintf(":%d", server.Config.Port)); err != nil {
			log.Fatalf("Failed to start HTTP server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := router.Shutdown(ctx); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Failed to gracefully shut down HTTP server: %v", err)
	}
}

// https://stackoverflow.com/questions/47606761/repeat-code-if-an-error-occured
func retry(attempts int, sleep time.Duration, f func() error) (err error) {

	for i := 0; ; i++ {
		err = f()
		if err == nil {
			return
		}

		if i >= (attempts - 1) {
			break
		}

		time.Sleep(sleep)

		log.Println("retrying after error:", err)
	}

	return fmt.Errorf("after %d attempts, last error: %s", attempts, err)
}
