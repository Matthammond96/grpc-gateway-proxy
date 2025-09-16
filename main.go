package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	GRPCProxy "github.com/nosana/grpc-to-http1-translation/proxies/grpc"
	HTTPProxy "github.com/nosana/grpc-to-http1-translation/proxies/http"
)

var rootCmd = &cobra.Command{
	Use: "grpc-gateway-proxy",
	Short: "A bi-directional communication between gRPC clients and servers over HTTP/1.1",
	Long:  `A protocol-agnostic, production-ready proxy that bridges gRPC and HTTP/1.1. It enables seamless, bi-directional communication between gRPC clients and servers over HTTP/1.1, making it easy to integrate gRPC services with legacy systems, load balancers, and environments where HTTP/2 is not available..`,
}

func main() {
	var grpcPort int
	var remoteHTTPPort int
	var httpPort int
	var grpcServicePort int

	var grpcProxyCmd = &cobra.Command{
		Use:   "start-grpc-proxy",
		Short: "Start the gRPC to HTTP proxy",
		Run: func(cmd *cobra.Command, args []string) {
			GRPCProxy.Start(grpcPort, remoteHTTPPort)
		},
	}
	grpcProxyCmd.Flags().IntVar(&grpcPort, "port", 9090, "The gRPC proxy listen port")
	grpcProxyCmd.Flags().IntVar(&remoteHTTPPort, "remote_http_port", 8080, "The remote HTTP proxy port")

	var httpProxyCmd = &cobra.Command{
		Use:   "start-http-proxy",
		Short: "Start the HTTP to gRPC proxy",
		Run: func(cmd *cobra.Command, args []string) {
			HTTPProxy.Start(httpPort, grpcServicePort)
		},
	}
	httpProxyCmd.Flags().IntVar(&httpPort, "port", 8080, "The HTTP proxy listen port")
	httpProxyCmd.Flags().IntVar(&grpcServicePort, "grpc_service_port", 50051, "The gRPC backend service port")

	rootCmd.AddCommand(grpcProxyCmd)
	rootCmd.AddCommand(httpProxyCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}