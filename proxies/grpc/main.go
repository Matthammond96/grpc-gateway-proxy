package GRPCProxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	pb "github.com/nosana/grpc-to-http1-translation/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	definitions "github.com/nosana/grpc-to-http1-translation/definitions"
)

type server struct {
	pb.UnimplementedMyserviceServer
}

type GRPCToHTTPConverter struct {
	targetURL  string
	connPool   sync.Pool
	httpClient *http.Client
}


func NewGRPCToHTTPConverter(targetURL string) *GRPCToHTTPConverter {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}

	return &GRPCToHTTPConverter{
		targetURL:  targetURL,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
	}
}

func (c *GRPCToHTTPConverter) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		log.Printf("UnaryInterceptor: Forwarding gRPC call to HTTP endpoint %s", c.targetURL)

		reqMsg, ok := req.(proto.Message)
		if !ok {
			return nil, status.Error(codes.Internal, "request is not a proto message")
		}

		reqData, err := proto.Marshal(reqMsg)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to marshal request: %v", err)
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", c.targetURL, bytes.NewReader(reqData))
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to create HTTP request: %v", err)
		}

		httpReq.Header.Set("Content-Type", definitions.ContentTypeProtobuf)
		httpReq.Header.Set(definitions.HeaderGRPCMethod, getMethodName(info.FullMethod))
		httpReq.Header.Set(definitions.HeaderGRPCService, getServiceName(info.FullMethod))

		if md, ok := metadata.FromIncomingContext(ctx); ok {
			for key, values := range md {
				if strings.HasPrefix(key, ":") {
					key = "colon-" + key[1:]
				}
				for _, value := range values {
					httpReq.Header.Add(definitions.HeaderGRPCMetadata+key, value)
				}
			}
		}

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			return nil, status.Error(codes.Unavailable, "HTTP request failed: "+err.Error())
		}

		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, status.Error(codes.Internal, fmt.Sprintf("HTTP error: %d", resp.StatusCode))
		}

		respData, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, status.Error(codes.Internal, "Failed to read HTTP response")
		}

		reply := reflect.New(reflect.TypeOf(req).Elem()).Interface().(proto.Message)
		err = proto.Unmarshal(respData, reply)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to unmarshal response: %v", err)
		}

		return reply, nil
	}
}

type streamWrapper struct {
	grpc.ServerStream
	pipeWriter *io.PipeWriter
	respChan   chan *http.Response
	errChan    chan error
}


func (c *GRPCToHTTPConverter) StreamInterceptor() grpc.StreamServerInterceptor {
	return func (srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		pr, pw := io.Pipe()

		httpReq, err := http.NewRequestWithContext(ss.Context(), "POST", c.targetURL, pr)
		if err != nil {
			return status.Error(codes.Internal, "Failed to create HTTP request")
		}

		httpReq.Header.Set("Content-Type", definitions.ContentTypeProtobufStream)
		httpReq.Header.Set("Transfer-Encoding", "chunked")
		httpReq.Header.Set(definitions.HeaderGRPCMethod, getMethodName(info.FullMethod))
		httpReq.Header.Set(definitions.HeaderGRPCService, getServiceName(info.FullMethod))

		respChan := make(chan *http.Response, 1)
		errChan := make(chan error, 1)

		go func() {
			resp, err := c.httpClient.Do(httpReq)
			if err != nil {
				errChan <- err
				return
			}

			respChan <- resp
		}()

		streamWrapper := &streamWrapper{
			ServerStream: ss,
			pipeWriter: pw,
			respChan: respChan,
			errChan: errChan,
		}

		return handler(srv, streamWrapper)
	}
}

func getMethodName(FullMethod string) string {
	parts := strings.Split(FullMethod, "/")
	if len(parts) >= 3 {
		return parts[2]
	}
	return ""
}

func getServiceName(FullMethod string) string {
	parts := strings.Split(FullMethod, "/")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

func Start(grpc_proxy_port int, http_proxy_port int) *grpc.Server {

	grpcToHTTP := NewGRPCToHTTPConverter(fmt.Sprintf("http://0.0.0.0:%d", http_proxy_port))

	s := grpc.NewServer(
		grpc.UnaryInterceptor(grpcToHTTP.UnaryInterceptor()),
		grpc.StreamInterceptor(grpcToHTTP.StreamInterceptor()),
	)
	pb.RegisterMyserviceServer(s, &server{})

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", grpc_proxy_port))

	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	log.Printf("gRPC server listening on port %d", grpc_proxy_port)
	
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}

	return s
}
