FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/starlark-actions ./...

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/starlark-actions /usr/local/bin/starlark-actions
ENTRYPOINT ["starlark-actions"]
