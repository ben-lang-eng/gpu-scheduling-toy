# gpu-scheduling-toy

A small Go service that models a pool of interchangeable GPUs and hands them out
over an HTTP API. Callers allocate a GPU, use it, and release it; when the pool
is empty, they can either fail fast or wait their turn. Built to explore the
concurrency, container, and Kubernetes patterns behind a GPU scheduling platform.

> **Scope:** this is a deliberately small learning project, not production
> software. The design notes below are explicit about its limitations.

## What it demonstrates

- Concurrency-safe resource pooling in Go using a channel as a counting semaphore
- Non-blocking (`TryAcquire`) and blocking, cancellable (`Acquire`) reservation
- A thin HTTP layer over a fully unit-tested core
- Graceful shutdown on `SIGINT`/`SIGTERM`, draining in-flight requests
- A minimal, non-root, distroless container image via a multi-stage build
- A Kubernetes deployment with liveness/readiness probes and a hardened pod
- CI that formats, vets, race-tests, and builds on every push

## Architecture

    main.go              wiring + graceful shutdown
    internal/pool        the GPU pool (domain logic, no HTTP)
    internal/server      HTTP handlers over the pool
    Dockerfile           multi-stage build -> distroless image
    k8s/                 Deployment + Service manifests
    .github/workflows    CI pipeline

The `pool` package has no knowledge of HTTP, so it is tested in isolation,
including a concurrency stress test run under the race detector. The `server`
package translates HTTP requests into pool operations and back into JSON.

## API

| Method | Path             | Description                          | Success | Failure |
|--------|------------------|--------------------------------------|---------|---------|
| GET    | `/healthz`       | Liveness probe                       | 200     | —       |
| GET    | `/readyz`        | Readiness probe                      | 200     | —       |
| GET    | `/stats`         | Pool utilisation as JSON             | 200     | —       |
| POST   | `/allocate`      | Reserve a GPU                        | 200     | 503 (full) |
| POST   | `/release/{id}`  | Return a GPU to the pool             | 204     | 400 (bad id), 409 (not allocated) |

Example:

    $ curl -s localhost:8080/stats
    {"capacity":8,"available":8,"inUse":0}
    $ curl -s -X POST localhost:8080/allocate
    {"gpu":0}

## Configuration

| Variable    | Default | Description                  |
|-------------|---------|------------------------------|
| `PORT`      | `8080`  | HTTP listen port             |
| `GPU_COUNT` | `8`     | Number of GPUs in the pool   |

## Run locally

    go run .
    # in another terminal:
    curl -s localhost:8080/stats

## Test

    go test ./...
    go test -race ./...   # includes a concurrent stress test

## Container

    docker build -t gpu-scheduling-toy:dev .
    docker run --rm -p 8080:8080 gpu-scheduling-toy:dev

## Kubernetes (local, via kind)

    kind create cluster --name gpu-toy
    docker build -t gpu-scheduling-toy:dev .
    kind load docker-image gpu-scheduling-toy:dev --name gpu-toy
    kubectl apply -f k8s/
    kubectl port-forward service/gpu-scheduling-toy 8080:80

## Design notes and limitations

- **Channel-as-semaphore.** The pool is a buffered channel holding the
  identifiers of free GPUs. Acquiring receives an id; releasing sends it back.
  This makes every operation safe for concurrent use with no explicit locks.

- **Release cannot detect every misuse.** The pool tracks only *how many* GPUs
  are free, not *which specific ones* are outstanding. Releasing an in-range id
  that was never acquired can therefore succeed and corrupt the count, as long
  as the pool is not already full. Releasing into a full pool is caught and
  returns an error. A production version would track outstanding reservations in
  a set (guarded by a mutex) to validate every release, trading the lock-free
  design for stricter accounting.

- **Per-replica state.** Each replica holds its own in-memory pool; they do not
  share state. Running multiple replicas is useful here only to demonstrate
  Kubernetes mechanics. A real scheduler would keep pool state in a shared
  store.

## Licence

MIT — see [LICENSE](LICENSE).