FROM oven/bun:1.4.3-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/bun.lock ./
RUN bun install --frozen-lockfile
COPY frontend/ ./
RUN bun run build

FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY ui/*.go ./ui/
COPY internal/ ./internal/
COPY --from=frontend /src/ui/dist ./ui/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags="-X main.version=${VERSION}" -o /metadonalds ./cmd/metadonalds

FROM rclone/rclone:1.75.1 AS rclone

FROM alpine:3.24
RUN apk add --no-cache ca-certificates
COPY --from=rclone /usr/local/bin/rclone /usr/local/bin/rclone
COPY --from=build /metadonalds /usr/local/bin/metadonalds
ENTRYPOINT ["/usr/local/bin/metadonalds"]
CMD ["serve"]
