FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/admin-api ./

FROM alpine:3.20
RUN adduser -D -H -s /sbin/nologin app
USER app
WORKDIR /app
COPY --from=build /out/admin-api /usr/local/bin/admin-api
EXPOSE 8095
ENTRYPOINT ["/usr/local/bin/admin-api"]
