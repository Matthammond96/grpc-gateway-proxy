# grpc-gateway-proxy

A protocol-agnostic, production-ready proxy that bridges gRPC and HTTP/1.1. grpc-gateway-proxy enables seamless, bi-directional communication between gRPC clients and servers over HTTP/1.1, making it easy to integrate gRPC services with legacy systems, load balancers, and environments where HTTP/2 is not available.

## Features

- **Generic**: Works with any gRPC service and proto definition
- **Bi-directional**: Supports both gRPC-to-HTTP and HTTP-to-gRPC flows
- **Simple deployment**: Run as a standalone binary or Docker container
- **Production-ready**: Designed for reliability and performance

## Use Cases

- Expose gRPC services to HTTP/1.1-only clients or networks
- Integrate gRPC microservices with legacy HTTP/1.1 infrastructure
- Bridge gRPC and RESTful APIs in hybrid cloud or service mesh environments

## Getting Started

### Prerequisites

- Go 1.20+
- Docker (optional, for containerized deployment)

### Build and Run

#### Locally

```sh
git clone https://github.com/Matthammond96/grpc-gateway-proxy.git
cd grpc-gateway-proxy
go build -o grpc-gateway-proxy ./main.go
./grpc-gateway-proxy start-grpc-proxy start-http-proxy
```

#### With Docker

```sh
docker build -t grpc-gateway-proxy .
docker run -p 8080:8080 -p 9090:9090 grpc-gateway-proxy start-grpc-proxy start-http-proxy
```

### Configuration

You can configure ports using flags:

- `--grpc_service_port` (default: 50051): gRPC backend server port
- `--grpc_proxy_port` (default: 9090): gRPC-to-HTTP proxy port
- `--http_proxy_port` (default: 8080): HTTP-to-gRPC proxy port

Example:

```sh
./grpc-gateway-proxy --grpc_service_port=50051 --grpc_proxy_port=9090 --http_proxy_port=8080 start-grpc-proxy start-http-proxy
```

## How It Works

- **gRPC-to-HTTP**: Accepts gRPC requests, translates them to HTTP/1.1, and forwards to the HTTP proxy.
- **HTTP-to-gRPC**: Accepts HTTP/1.1 requests, translates them to gRPC, and forwards to the gRPC backend.
- Uses dynamic protobuf reflection for proto-agnostic operation.

## Contributing

Contributions, issues, and feature requests are welcome! Please open an issue or pull request.

## License

MIT License
