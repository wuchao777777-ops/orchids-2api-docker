FROM golang:1.24-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOMAXPROCS=1 \
    go build -p 1 -trimpath -ldflags="-s -w" -o /out/orchids-server ./cmd/server

FROM alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /out/orchids-server /usr/local/bin/orchids-server
COPY config.docker.json /app/config.json

RUN mkdir -p /app/data/tmp/image /app/data/tmp/video && \
    addgroup -g 10001 -S orchids && \
    adduser -u 10001 -S -D -h /app -s /sbin/nologin -G orchids orchids && \
    chown -R orchids:orchids /app

USER orchids

EXPOSE 3002

ENTRYPOINT ["orchids-server"]
CMD ["-config", "/app/config.json"]
