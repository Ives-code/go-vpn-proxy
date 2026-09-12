FROM golang:1.26.8-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build \
    -tags "with_quic with_utls with_grpc" \
    -trimpath -ldflags="-s -w" \
    -o /out/dual-egress-gateway ./cmd/dual-egress-gateway

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/dual-egress-gateway /usr/local/bin/dual-egress-gateway
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/dual-egress-gateway"]
CMD ["-config", "/etc/dual-egress-gateway/config.yaml"]
