# ---- Build stage ----
# Use the official Go image to compile the binary. This stage is discarded
# after the build, so its size does not affect the final image.
FROM golang:1.26 AS build

WORKDIR /src

# Copy the module definition first and download dependencies. Docker caches
# this layer and only re-runs it when go.mod changes, so day-to-day rebuilds
# skip dependency resolution.
COPY go.mod ./
RUN go mod download

# Copy the rest of the source and compile a statically linked Linux binary.
# CGO_ENABLED=0 removes the C library dependency so the binary can run in an
# image that contains nothing but the binary itself.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/gpu-scheduling-toy .

# ---- Runtime stage ----
# distroless/static contains no shell, package manager, or libc — only the files
# needed to run a static binary. Smaller attack surface, smaller image.
FROM gcr.io/distroless/static-debian12:nonroot

# Copy only the compiled binary out of the build stage.
COPY --from=build /out/gpu-scheduling-toy /gpu-scheduling-toy

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/gpu-scheduling-toy"]