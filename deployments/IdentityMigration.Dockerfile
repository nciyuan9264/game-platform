FROM golang:1.24-alpine AS builder

WORKDIR /src
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false \
    -o /out/normalize-player-identities ./cmd/normalize-player-identities && \
    CGO_ENABLED=0 GOOS=linux go build -buildvcs=false \
    -o /out/backfill-replay ./cmd/backfill-replay

FROM postgres:17-alpine
COPY --from=builder /out/normalize-player-identities /usr/local/bin/normalize-player-identities
COPY --from=builder /out/backfill-replay /usr/local/bin/backfill-replay
ENTRYPOINT ["/usr/local/bin/normalize-player-identities"]
