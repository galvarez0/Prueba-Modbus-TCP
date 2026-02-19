FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/clickhouse-ingestor ./...

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/clickhouse-ingestor /usr/local/bin/clickhouse-ingestor
ENTRYPOINT ["clickhouse-ingestor"]
