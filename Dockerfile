# ============================================================
# Stage 1: Build the picoclaw binary
# ============================================================
FROM golang:1.26.0-alpine AS builder

RUN apk add --no-cache git make

WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN go generate ./... && CGO_ENABLED=0 go build -o /picoclaw ./cmd/picoclaw/

# ============================================================
# Stage 2: Runtime with Python/ffmpeg for TTS
# ============================================================
FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      python3 python3-pip ffmpeg ca-certificates tzdata curl poppler-utils && \
    pip3 install edge-tts --break-system-packages && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

ENV TZ=America/Argentina/Buenos_Aires
ENV GH_CONFIG_DIR=/hostfs/home/diego/.config/gh

# Symlink host gh binary (available via /hostfs bind mount)
RUN ln -s /hostfs/usr/bin/gh /usr/local/bin/gh

# Copy binary
COPY --from=builder /picoclaw /usr/local/bin/picoclaw

ENTRYPOINT ["picoclaw"]
CMD ["gateway"]
