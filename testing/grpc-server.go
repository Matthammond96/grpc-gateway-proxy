package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"

	pb "github.com/nosana/grpc-to-http1-translation/proto"
	"google.golang.org/grpc"
)

var (
	grpc_service_port = flag.Int("grpc_service_port", 50051, "The grpc service port")
	grpc_proxy_port = flag.Int("grpc_proxy_port", 9090, "The GRPC -> REST proxy listener port")
	http_proxy_port = flag.Int("http_proxy_port", 8080, "The REST -> GRPC proxy listener port")
)

type server struct {
	pb.UnimplementedMyserviceServer
}

func (s *server) SayHello(_ context.Context, in *pb.HelloRequest) (*pb.HelloResponse, error) {
	log.Printf("Received: %v", in.GetName())
	return &pb.HelloResponse{Message: "Hello " + in.GetName()}, nil
}

func main() {
	flag.Parse()

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *grpc_service_port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterMyserviceServer(grpcServer, &server{})

	log.Printf("gRPC server listening on port %d", *grpc_service_port)

	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
	
}