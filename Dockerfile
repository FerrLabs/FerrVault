# Build the manager binary.
FROM docker.io/library/golang:1.23-bookworm AS builder

WORKDIR /workspace

# Source first — `go mod tidy` below needs to see the imports to resolve
# transitive deps. Layer caching isn't as tight as a separate `go mod
# download` step, but we don't yet commit `go.sum`, so download would fail
# (Go 1.21+ requires checksums). Once `go.sum` is committed this block can
# move back to the classic deps-first layout.
COPY go.mod go.sum* ./
COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/

RUN go mod tidy

# CGO_ENABLED=0 produces a static binary we can put on distroless below.
# TARGETOS / TARGETARCH are set automatically by buildx for multi-arch builds;
# defaulting to linux/amd64 when built without buildx (e.g. a direct
# `docker build`).
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags='-s -w' -o /out/manager ./cmd

# Distroless runtime — no shell, tiny attack surface.
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /out/manager /manager
USER 65532:65532
ENTRYPOINT ["/manager"]
