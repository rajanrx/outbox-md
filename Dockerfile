# --- web build ---
FROM node:20-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# --- go build (embeds web/dist via main wiring) ---
FROM golang:1.25-alpine AS go
WORKDIR /src
COPY go.* ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 go build -o /outbox-md ./cmd/outbox-md

# --- runtime ---
FROM gcr.io/distroless/static-debian12
COPY --from=go /outbox-md /outbox-md
EXPOSE 8181
VOLUME ["/data"]
ENTRYPOINT ["/outbox-md"]
