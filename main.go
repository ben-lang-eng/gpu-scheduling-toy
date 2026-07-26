// Command gpu-scheduling-toy runs an HTTP server that hands out a fixed pool of
// simulated GPUs, letting callers allocate and release them over a JSON API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/ben-lang-eng/gpu-scheduling-toy/internal/pool"
	"github.com/ben-lang-eng/gpu-scheduling-toy/internal/server"
)

const (
	defaultPort         = "8080"
	defaultGPUCount     = 8
	readTimeout         = 5 * time.Second
	writeTimeout        = 10 * time.Second
	shutdownGracePeriod = 10 * time.Second
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("gpu-scheduling-toy: %v", err)
	}
}

func run() error {
	gpuCount := envInt("GPU_COUNT", defaultGPUCount)
	port := envString("PORT", defaultPort)

	gpuPool, err := pool.New(gpuCount)
	if err != nil {
		return fmt.Errorf("creating GPU pool: %w", err)
	}

	httpServer := &http.Server{
		Addr:         ":" + port,
		Handler:      server.New(gpuPool),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}

	// Cancel the base context when an interrupt or termination signal arrives,
	// so the server can begin a graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Serve in a separate goroutine so run can wait for either a fatal serve
	// error or a shutdown signal, whichever comes first.
	serveErr := make(chan error, 1)
	go func() {
		log.Printf("listening on %s with %d GPUs", httpServer.Addr, gpuCount)
		serveErr <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("server failed: %w", err)
		}
		return nil
	case <-ctx.Done():
		log.Println("shutdown signal received, draining connections")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGracePeriod)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown failed: %w", err)
		}
		return nil
	}
}

// envString returns the value of the named environment variable, or fallback
// when it is unset or empty.
func envString(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// envInt returns the integer value of the named environment variable, or
// fallback when it is unset, empty, or not a valid integer.
func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return fallback
	}
	return value
}
