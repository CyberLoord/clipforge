FROM node:24-bookworm-slim AS web
WORKDIR /web
RUN corepack enable && corepack prepare pnpm@11.25.0 --activate
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.27.2-bookworm AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /clipforge ./cmd/clipforge

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg fonts-dejavu-core ca-certificates curl && rm -rf /var/lib/apt/lists/*
RUN useradd --system --uid 10001 --create-home clipforge && mkdir -p /app/data && chown clipforge:clipforge /app/data
WORKDIR /app
COPY --from=backend /clipforge /usr/local/bin/clipforge
COPY --from=web /web/dist ./web/dist
COPY migrations/ ./migrations/
USER clipforge
ENV ADDR=0.0.0.0:8080 DATA_DIR=/app/data
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s CMD curl -fsS http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["clipforge"]
