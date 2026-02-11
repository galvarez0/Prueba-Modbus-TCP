FROM golang:1.23-alpine AS build
RUN apk add --no-cache git make
WORKDIR /src
RUN git clone https://github.com/brocaar/chirpstack-simulator.git .
RUN make clean build

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /src/build/chirpstack-simulator /usr/local/bin/chirpstack-simulator
ENTRYPOINT ["chirpstack-simulator"]