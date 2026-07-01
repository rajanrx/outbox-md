# --- web build (arch-independent JS; always run on the native build host) ---
FROM --platform=$BUILDPLATFORM node:20-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# --- go build (embeds web/dist via main wiring) ---
# Run the Go toolchain on the NATIVE build host and cross-compile to the target
# arch (CGO disabled → fully static). This avoids QEMU emulation, so multi-arch
# `buildx` builds stay fast instead of hanging on an emulated arm64 build.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS go
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.* ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /outbox-md ./cmd/outbox-md

# --- runtime ---
FROM gcr.io/distroless/static-debian12
COPY --from=go /outbox-md /outbox-md
EXPOSE 8181
VOLUME ["/data"]
# The local CLI defaults -dir to the current directory, but the distroless
# runtime has no working dir, so pin the served folder to the mounted /data
# volume. The arg-less ENTRYPOINT below runs `serve`, which reads OUTBOX_DIR.
ENV OUTBOX_DIR=/data
# Marks a container install so the CLI never tries to self-update an immutable
# image binary (Docker updates via image pull / Watchtower instead).
ENV OUTBOX_CONTAINER=1
ENTRYPOINT ["/outbox-md"]
