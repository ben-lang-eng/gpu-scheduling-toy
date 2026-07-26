// Package server exposes a GPU pool over a small HTTP JSON API.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/ben-lang-eng/gpu-scheduling-toy/internal/pool"
)

// Server routes HTTP requests to operations on an underlying GPU pool. It
// satisfies http.Handler, so it can be passed directly to an http.Server.
type Server struct {
	pool *pool.Pool
	mux  *http.ServeMux
}

// New builds a Server backed by gpuPool and registers its routes.
func New(gpuPool *pool.Pool) *Server {
	srv := &Server{
		pool: gpuPool,
		mux:  http.NewServeMux(),
	}
	srv.routes()
	return srv
}

// ServeHTTP satisfies http.Handler by delegating to the internal router.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	srv.mux.ServeHTTP(w, r)
}

// handleHealth reports process liveness. It always succeeds while the server is
// running, and backs a Kubernetes liveness probe ("is the process alive?").
func (srv *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "ok")
}

// handleReady reports readiness to serve traffic, and backs a Kubernetes
// readiness probe ("should this instance receive requests?").
func (srv *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "ready")
}

// routes registers every HTTP route the server handles. Centralising
// registration here means the full API surface can be read in one place.
func (srv *Server) routes() {
	srv.mux.HandleFunc("GET /healthz", srv.handleHealth)
	srv.mux.HandleFunc("GET /readyz", srv.handleReady)
	srv.mux.HandleFunc("GET /stats", srv.handleStats)
	srv.mux.HandleFunc("POST /allocate", srv.handleAllocate)
	srv.mux.HandleFunc("POST /release/{id}", srv.handleRelease)
}

// writeJSON encodes value as JSON and writes it as the response body with the
// given status code. Centralising encoding here keeps the Content-Type header
// and error handling consistent across every handler that returns JSON.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		// The status line and part of the body may already be sent, so the
		// response cannot be corrected; log the failure for observability.
		log.Printf("writeJSON: encoding response: %v", err)
	}
}

// handleStats returns a snapshot of pool utilisation as JSON.
func (srv *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, srv.pool.Stats())
}

// errorResponse is the JSON body returned when a request cannot be satisfied.
type errorResponse struct {
	Error string `json:"error"`
}

// allocateResponse is the JSON body returned when a GPU is allocated.
type allocateResponse struct {
	GPU pool.GPU `json:"gpu"`
}

// handleAllocate reserves a free GPU and returns its identifier as JSON. It
// responds with 503 Service Unavailable when every GPU is already in use.
func (srv *Server) handleAllocate(w http.ResponseWriter, r *http.Request) {
	gpu, err := srv.pool.TryAcquire()
	switch {
	case errors.Is(err, pool.ErrNoCapacity):
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "no GPU available"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal error"})
	default:
		writeJSON(w, http.StatusOK, allocateResponse{GPU: gpu})
	}
}

// handleRelease returns a GPU to the pool. The GPU identifier is taken from the
// request path. It responds with 400 Bad Request for a malformed or out-of-range
// identifier, and 409 Conflict if the GPU was not currently allocated.
func (srv *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 0 || id >= srv.pool.Capacity() {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid GPU id"})
		return
	}

	if err := srv.pool.Release(pool.GPU(id)); errors.Is(err, pool.ErrNotReserved) {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "GPU was not allocated"})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
