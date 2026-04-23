# Build the manager binary.
FROM docker.io/library/golang:1.24-bookworm AS builder

WORKDIR /workspace

# Deps first for tight layer caching: this layer only invalidates when go.mod
# or go.sum change, not on every source edit.
COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/

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
