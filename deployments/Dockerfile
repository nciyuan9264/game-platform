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

FROM debian:bookworm-slim
WORKDIR /app
COPY --from=builder /out/service /app/service
EXPOSE 8080 9000 18092 18093
ENTRYPOINT ["/app/service"]
