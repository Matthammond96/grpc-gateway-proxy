package GRPCProxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/jhump/protoreflect/desc"
	definitions "github.com/nosana/grpc-gateway-proxy/definitions"
)

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
		targetURL: targetURL,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
	}
}

func (c *GRPCToHTTPConverter) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		log.Printf("[grpc-proxy] UnaryInterceptor: Forwarding gRPC call to HTTP endpoint %s", c.targetURL)
		log.Printf("[grpc-proxy] Incoming gRPC request: FullMethod=%s, req type=%T", info.FullMethod, req)

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

		// Return raw bytes for the dynamic handler to decode
		return respData, nil
	}
}

type streamWrapper struct {
	grpc.ServerStream
	pipeWriter *io.PipeWriter
	respChan   chan *http.Response
	errChan    chan error
}

func (c *GRPCToHTTPConverter) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
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
			pipeWriter:   pw,
			respChan:     respChan,
			errChan:      errChan,
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

func Start(grpc_proxy_port int, http_proxy_server_address string, protoFiles []string) *grpc.Server {

	// Create the gRPC server with your interceptors
	grpcToHTTP := NewGRPCToHTTPConverter(http_proxy_server_address)
	s := grpc.NewServer()

	// Register all loaded file descriptors and message types with the global registries
	var allFiles []*desc.FileDescriptor
	for _, protoPath := range protoFiles {
		fileBytes, err := os.ReadFile(protoPath)
		if err != nil {
			log.Fatalf("failed to read proto file %s: %v", protoPath, err)
		}
		var fds descriptorpb.FileDescriptorSet
		if err := proto.Unmarshal(fileBytes, &fds); err != nil {
			log.Fatalf("failed to unmarshal FileDescriptorSet from %s: %v", protoPath, err)
		}
		files, err := desc.CreateFileDescriptorsFromSet(&fds)
		if err != nil {
			log.Fatalf("failed to create file descriptors from set: %v", err)
		}
		for _, fd := range files {
			allFiles = append(allFiles, fd)
			// Register file descriptor with global registry
			_ = protoregistry.GlobalFiles.RegisterFile(fd.UnwrapFile())
			// Register all message types
			for _, msg := range fd.GetMessageTypes() {
				if md, ok := msg.Unwrap().(protoreflect.MessageDescriptor); ok {
					_ = protoregistry.GlobalTypes.RegisterMessage(dynamicpb.NewMessageType(md))
				}
			}
		}
	}

	// Now dynamically register all services and methods
	for _, fd := range allFiles {
		for _, svc := range fd.GetServices() {
			// Build grpc.ServiceDesc dynamically
			methods := []grpc.MethodDesc{}
			streams := []grpc.StreamDesc{}
			for _, m := range svc.GetMethods() {
				methodDesc := m // capture for closure
				// Fix Go loop variable capture bug by copying to local variables
				methodName := methodDesc.GetName()
				fullMethod := fmt.Sprintf("/%s/%s", svc.GetFullyQualifiedName(), methodName)
				inputType := methodDesc.GetInputType()
				outputType := methodDesc.GetOutputType()
				// Copy for closure
				localMethodName := methodName
				localFullMethod := fullMethod
				localInputType := inputType
				localOutputType := outputType
				if !methodDesc.IsServerStreaming() && !methodDesc.IsClientStreaming() {
					// Unary method
					methods = append(methods, grpc.MethodDesc{
						MethodName: localMethodName,
						Handler: func(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
							log.Printf("[grpc-proxy] Handler called for method: %s", localFullMethod)
							// Build input message
							mt, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(localInputType.GetFullyQualifiedName()))
							if err != nil {
								return nil, status.Errorf(codes.Internal, "failed to find input message descriptor: %v", err)
							}
							msg := dynamicpb.NewMessage(mt.Descriptor())
							if err := dec(msg); err != nil {
								return nil, err
							}
							info := &grpc.UnaryServerInfo{
								Server:     srv,
								FullMethod: localFullMethod,
							}
							handler := func(ctx context.Context, reqi interface{}) (interface{}, error) {
								log.Printf("[grpc-proxy] Handler called for method: %s", localFullMethod)
								// Call the HTTP proxy and decode response into correct output type
								resp, err := grpcToHTTP.UnaryInterceptor()(ctx, reqi, info, nil)
								if err != nil {
									fmt.Printf("[grpc-proxy] ERROR: HTTP proxy call failed: %v\n", err)
									return nil, err
								}
								ot, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(localOutputType.GetFullyQualifiedName()))
								if err != nil {
									fmt.Printf("[grpc-proxy] ERROR: failed to find output message descriptor: %v\n", err)
									return nil, status.Errorf(codes.Internal, "failed to find output message descriptor: %v", err)
								}
								reply := dynamicpb.NewMessage(ot.Descriptor())
								// Debug: print output type and response type
								fmt.Printf("[grpc-proxy] DEBUG: outputType: %s, resp type: %T\n", localOutputType.GetFullyQualifiedName(), resp)
								v, ok := resp.([]byte)
								if !ok {
									fmt.Printf("[grpc-proxy] FATAL: handler returned non-[]byte type: %T\n", resp)
									panic(fmt.Sprintf("handler returned non-[]byte type: %T", resp))
								}
								fmt.Printf("[grpc-proxy] DEBUG: response bytes len=%d, hex=%x\n", len(v), v)
								// Print first 64 bytes as string for debugging (may reveal JSON or proto)
								previewLen := 64
								if len(v) < previewLen {
									previewLen = len(v)
								}
								fmt.Printf("[grpc-proxy] DEBUG: response bytes (string preview): %q\n", string(v[:previewLen]))
								if err := proto.Unmarshal(v, reply); err != nil {
									fmt.Printf("[grpc-proxy] ERROR: failed to unmarshal response bytes: %v\n", err)
									return nil, status.Errorf(codes.Internal, "failed to unmarshal response: %v", err)
								}
								fmt.Printf("[grpc-proxy] DEBUG: successfully unmarshaled response into %s\n", localOutputType.GetFullyQualifiedName())
								return reply, nil
							}
							if interceptor == nil {
								return handler(ctx, msg)
							}
							return interceptor(ctx, msg, info, handler)
						},
					})
				} else {
					// Streaming method (not yet implemented)
					streams = append(streams, grpc.StreamDesc{
						StreamName: methodName,
						Handler: func(srv interface{}, stream grpc.ServerStream) error {
							// TODO: Implement stream forwarding logic
							return status.Errorf(codes.Unimplemented, "streaming proxy not implemented")
						},
						ServerStreams: methodDesc.IsServerStreaming(),
						ClientStreams: methodDesc.IsClientStreaming(),
					})
				}
			}
			s.RegisterService(&grpc.ServiceDesc{
				ServiceName: svc.GetFullyQualifiedName(),
				HandlerType: nil,
				Methods:     methods,
				Streams:     streams,
				Metadata:    fd.GetName(),
			}, nil)
		}
	}

	log.Printf("[grpc-proxy] Registered services and methods:")
	for _, fd := range allFiles {
		for _, svc := range fd.GetServices() {
			log.Printf("[grpc-proxy] Service: %s", svc.GetFullyQualifiedName())
			for _, m := range svc.GetMethods() {
				log.Printf("[grpc-proxy]   Method: %s", m.GetName())
			}
		}
	}

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
