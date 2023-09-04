package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/allaboutapps/integresql/internal/api"
	"github.com/allaboutapps/integresql/internal/config"
	"github.com/allaboutapps/integresql/internal/router"
)

func main() {

	fmt.Println(config.GetFormattedBuildArgs())

	s := api.DefaultServerFromEnv()

	if err := s.InitManager(context.Background()); err != nil {
		log.Fatalf("Failed to initialize manager: %v", err)
	}

	router.Init(s)

	go func() {
		if err := s.Start(); err != nil {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Failed to gracefully shut down server: %v", err)
	}
}
