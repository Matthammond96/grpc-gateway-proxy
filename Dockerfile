
FROM golang:1.24.6 AS builder

COPY . .
RUN make


FROM ubuntu:22.04

RUN apt-get update && \
	apt-get install -y wget unzip && \
	wget -q https://github.com/protocolbuffers/protobuf/releases/download/v25.3/protoc-25.3-linux-x86_64.zip && \
	unzip protoc-25.3-linux-x86_64.zip -d /usr/local && \
	rm protoc-25.3-linux-x86_64.zip && \
	apt-get purge -y --auto-remove wget unzip && \
	apt-get clean && rm -rf /var/lib/apt/lists/*

COPY --from=builder /go/bin/grpc-gateway-proxy /usr/local/bin/grpc-gateway-proxy

ENTRYPOINT ["grpc-gateway-proxy"]