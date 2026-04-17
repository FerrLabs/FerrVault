# Build the manager binary.
FROM docker.io/library/golang:1.23-bookworm AS builder

WORKDIR /workspace

# Deps first — better layer caching.
COPY go.mod go.sum* ./
RUN go mod download

# Source.
COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/

# CGO_ENABLED=0 produces a static binary we can put on distroless below.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags='-s -w' -o /out/manager ./cmd

# Distroless runtime — no shell, tiny attack surface.
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /out/manager /manager
USER 65532:65532
ENTRYPOINT ["/manager"]
