
FROM golang:1.24.6 AS builder

COPY . .
RUN make

FROM ubuntu:22.04

COPY --from=builder /go/bin/grpc-gateway-proxy /usr/local/bin/grpc-gateway-proxy

ENTRYPOINT ["grpc-gateway-proxy"]