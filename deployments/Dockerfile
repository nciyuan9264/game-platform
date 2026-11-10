FROM golang:1.24-bookworm AS builder

WORKDIR /src
ARG GOPROXY=https://goproxy.cn,direct
ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG http_proxy
ARG https_proxy
ENV GOPROXY=${GOPROXY}
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG SERVICE
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -o /out/service ./cmd/${SERVICE}
ARG EDAX_VERSION=v4.7
ARG EDAX_COMMIT=6c6991bee9a3d31d04f87934b04a7fd23c279f2b
ARG EDAX_EVAL_SHA256=f8b2299612d9fa4414157e70e932636e33111c2602d0c2fc382a7d90ef21b792
RUN set -eux; \
    mkdir -p /out/edax; \
    if [ "${SERVICE}" = "othello-edax-service" ]; then \
      apt-get update; \
      apt-get install -y --no-install-recommends ca-certificates gcc git make; \
      rm -rf /var/lib/apt/lists/*; \
      mkdir -p /tmp/edax /out/edax/bin /out/edax/data /out/edax/source; \
      git -C /tmp/edax init; \
      git -C /tmp/edax remote add origin \
        https://github.com/abulmo/edax-reversi.git; \
      git -C /tmp/edax fetch --depth 1 origin "${EDAX_COMMIT}"; \
      git -C /tmp/edax checkout --detach FETCH_HEAD; \
      test "$(git -C /tmp/edax rev-parse HEAD)" = "${EDAX_COMMIT}"; \
      echo "${EDAX_EVAL_SHA256}  /tmp/edax/bin/data/eval.dat" | sha256sum -c -; \
      make -C /tmp/edax/src build \
        ARCH=x86-64-v2 \
        CC=gcc \
        OS=linux \
        BIN=/out/edax/bin; \
      mv /out/edax/bin/lEdax-x86-64-v2 /out/edax/bin/edax; \
      cp /tmp/edax/bin/data/eval.dat /out/edax/data/eval.dat; \
      cp /tmp/edax/LICENSE /out/edax/LICENSE; \
      git -C /tmp/edax archive \
        --format=tar.gz \
        --prefix="edax-reversi-${EDAX_VERSION}/" \
        --output="/out/edax/source/edax-reversi-${EDAX_VERSION}.tar.gz" \
        HEAD; \
    fi

FROM debian:bookworm-slim
WORKDIR /app
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /out/service /app/service
COPY --from=builder /out/edax/ /opt/edax/
EXPOSE 8080 9000 18092 18093 18094 18095
ENTRYPOINT ["/app/service"]
