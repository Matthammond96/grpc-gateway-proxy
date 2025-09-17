package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	GRPCProxy "github.com/nosana/grpc-gateway-proxy/proxies/grpc"
	HTTPProxy "github.com/nosana/grpc-gateway-proxy/proxies/http"
)

var rootCmd = &cobra.Command{
	Use:   "grpc-gateway-proxy",
	Short: "A bi-directional communication between gRPC clients and servers over HTTP/1.1",
	Long:  `A protocol-agnostic, production-ready proxy that bridges gRPC and HTTP/1.1. It enables seamless, bi-directional communication between gRPC clients and servers over HTTP/1.1, making it easy to integrate gRPC services with legacy systems, load balancers, and environments where HTTP/2 is not available..`,
}

func ensureDescriptorSets(protoFiles []string) ([]string, error) {
	var outFiles []string
	for _, f := range protoFiles {
		if strings.HasSuffix(f, ".proto") {
			dir := filepath.Dir(f)
			base := filepath.Base(f)
			outBase := strings.TrimSuffix(base, ".proto") + ".protoset"
			out := filepath.Join(dir, outBase)
			protoInfo, err := os.Stat(f)
			if err != nil {
				return nil, fmt.Errorf("failed to stat proto file %s: %w", f, err)
			}
			needGen := false
			outInfo, err := os.Stat(out)
			if os.IsNotExist(err) {
				needGen = true
			} else if err != nil {
				return nil, fmt.Errorf("failed to stat protoset file %s: %w", out, err)
			} else if protoInfo.ModTime().After(outInfo.ModTime()) {
				needGen = true
			}
			if needGen {
				cmd := exec.Command("protoc", "--descriptor_set_out="+outBase, base)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				cmd.Dir = dir
				if err := cmd.Run(); err != nil {
					return nil, fmt.Errorf("failed to run protoc for %s: %w", f, err)
				}
			}
			outFiles = append(outFiles, out)
		} else {
			outFiles = append(outFiles, f)
		}
	}
	return outFiles, nil
}

func main() {
	var (
		grpc_listener_port int
		http_proxy_address string
	)

	var (
		http_listener_port   int
		grpc_service_address string
	)

	var protoFiles []string

	var grpcProxyCmd = &cobra.Command{
		Use:   "start-grpc-proxy",
		Short: "Start the gRPC to HTTP proxy",
		Run: func(cmd *cobra.Command, args []string) {
			files, err := ensureDescriptorSets(protoFiles)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to process proto files: %v\n", err)
				os.Exit(1)
			}
			GRPCProxy.Start(grpc_listener_port, http_proxy_address, files)
		},
	}

	grpcProxyCmd.Flags().IntVar(&grpc_listener_port, "grpc_listener_port", 9090, "The gRPC proxy listener port")
	grpcProxyCmd.Flags().StringVar(&http_proxy_address, "http_proxy_address", "http://localhost:8080", "The HTTP proxy server address")
	grpcProxyCmd.Flags().StringArrayVar(&protoFiles, "proto", []string{}, "Path to a proto file (repeatable)")

	var httpProxyCmd = &cobra.Command{
		Use:   "start-http-proxy",
		Short: "Start the HTTP to gRPC proxy",
		Run: func(cmd *cobra.Command, args []string) {
			files, err := ensureDescriptorSets(protoFiles)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to process proto files: %v\n", err)
				os.Exit(1)
			}
			HTTPProxy.Start(http_listener_port, grpc_service_address, files)
		},
	}

	httpProxyCmd.Flags().IntVar(&http_listener_port, "http_listener_port", 8080, "The HTTP proxy listener port")
	httpProxyCmd.Flags().StringVar(&grpc_service_address, "grpc_service_address", "0.0.0.0:50051", "The gRPC backend service address")
	httpProxyCmd.Flags().StringArrayVar(&protoFiles, "proto", []string{}, "Path to a proto file (repeatable)")

	rootCmd.AddCommand(grpcProxyCmd)
	rootCmd.AddCommand(httpProxyCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
