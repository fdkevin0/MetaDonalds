FROM golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -o /server .

FROM alpine:3.24
RUN apk add --no-cache ca-certificates
COPY --from=build /server /usr/local/bin/server
ENTRYPOINT ["/usr/local/bin/server"]
