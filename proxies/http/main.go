package HTTPProxy

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"os"

	definitions "github.com/nosana/grpc-gateway-proxy/definitions"
)

type ProtobufStreamDecoder struct {
	reader io.Reader
}

func NewProtobufStreamDecoder(r io.Reader) *ProtobufStreamDecoder {
	return &ProtobufStreamDecoder{reader: r}
}

func (d *ProtobufStreamDecoder) Decode() ([]byte, error) {
	var length uint32
	if err := binary.Read(d.reader, binary.BigEndian, &length); err != nil {
		return nil, err
	}

	data := make([]byte, length)
	_, err := io.ReadFull(d.reader, data)
	return data, err
}

type ProtobufStreamEncoder struct {
	writer io.Writer
}

func NewProtobufStreamEncoder(w io.Writer) *ProtobufStreamEncoder {
	return &ProtobufStreamEncoder{writer: w}
}

func (e *ProtobufStreamEncoder) Encode(data []byte) error {
	length := uint32(len(data))
	if err := binary.Write(e.writer, binary.BigEndian, length); err != nil {
		return err
	}

	_, err := e.writer.Write(data)
	return err
}

type HTTPToGRPCConverter struct {
	grpcConn *grpc.ClientConn
	connPool sync.Pool
}

func NewHTTPToGRPCConverter(grpcTarget string) (*HTTPToGRPCConverter, error) {
	conn, err := grpc.Dial(grpcTarget,
		grpc.WithInsecure(),
		grpc.WithBlock(),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:    30 * time.Second,
			Timeout: 5 * time.Second,
		}),
	)

	if err != nil {
		return nil, err
	}

	return &HTTPToGRPCConverter{
		grpcConn: conn,
	}, nil
}

func (c *HTTPToGRPCConverter) HTTPHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 {
			http.Error(w, "Malformed URL: expected /<service>/<method>", http.StatusBadRequest)
			return
		}
		service := parts[0]
		method := parts[1]

		contentType := r.Header.Get("Content-Type")

		switch contentType {
		case definitions.ContentTypeProtobuf:
			c.handleUnaryHTTP(w, r, service, method)
		case definitions.ContentTypeProtobufStream:
			c.handleStreamHTTP(w, r, service, method)
		default:
			http.Error(w, "Unsupported content type", http.StatusBadRequest)
		}
	}
}

func (c *HTTPToGRPCConverter) handleUnaryHTTP(w http.ResponseWriter, r *http.Request, service, method string) {
	reqData, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	md := metadata.New(nil)

	for key, values := range r.Header {
		if strings.HasPrefix(key, definitions.HeaderGRPCMetadata) {
			grpcKey := strings.TrimPrefix(key, definitions.HeaderGRPCMetadata)
			if strings.HasPrefix(grpcKey, "colon-") {
				grpcKey = strings.Replace(grpcKey, "colon-", ":", 1)
			}
			for _, value := range values {
				md.Append(grpcKey, value)
			}
		}
	}

	ctx = metadata.NewOutgoingContext(ctx, md)

	serviceName := service
	if strings.Contains(service, ".") {
		parts := strings.Split(service, ".")
		serviceName = parts[len(parts)-1]
	}
	files := protoregistry.GlobalFiles
	var fileDesc protoreflect.FileDescriptor
	files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if fd.Services().ByName(protoreflect.Name(serviceName)) != nil {
			fileDesc = fd
			return false
		}
		return true
	})
	if fileDesc == nil {
		log.Printf("Service not found for: %s", service)
		http.Error(w, "Service not found", http.StatusBadRequest)
		return
	}
	serviceDesc := fileDesc.Services().ByName(protoreflect.Name(serviceName))
	if serviceDesc == nil {
		log.Printf("Service descriptor not found for: %s", serviceName)
		http.Error(w, "Service not found", http.StatusBadRequest)
		return
	}
	methodDesc := serviceDesc.Methods().ByName(protoreflect.Name(method))
	if methodDesc == nil {
		log.Printf("Method descriptor not found for: %s", method)
		http.Error(w, "Method not found", http.StatusBadRequest)
		return
	}
	inputDesc := methodDesc.Input()
	reqMsg := dynamicpb.NewMessage(inputDesc)
	err = proto.Unmarshal(reqData, reqMsg)
	if err != nil {
		http.Error(w, "Failed to unmarshal request", http.StatusBadRequest)
		return
	}

	outputDesc := methodDesc.Output()
	respMsg := dynamicpb.NewMessage(outputDesc)
	err = c.grpcConn.Invoke(ctx, fmt.Sprintf("/%s/%s", service, method), reqMsg, respMsg)
	if err != nil {
		http.Error(w, "Failed to call gRPC method", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", definitions.ContentTypeProtobuf)
	respBytes, err := proto.Marshal(respMsg)
	if err != nil {
		http.Error(w, "Failed to marshal gRPC response", http.StatusInternalServerError)
		return
	}
	log.Printf("[http-proxy] Marshaled proto response bytes: %d bytes", len(respBytes))
	log.Printf("[http-proxy] Marshaled proto response hex: %x", respBytes)
	w.Write(respBytes)
}

func (c *HTTPToGRPCConverter) handleStreamHTTP(w http.ResponseWriter, r *http.Request, service, method string) {
	w.Header().Set("Content-Type", definitions.ContentTypeProtobufStream)
	w.Header().Set("Transfer-Encoding", "chunked")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	md := metadata.New(nil)

	for key, values := range r.Header {
		if strings.HasPrefix(key, definitions.HeaderGRPCMetadata) {
			grpcKey := strings.TrimPrefix(key, definitions.HeaderGRPCMetadata)
			if strings.HasPrefix(grpcKey, "colon-") {
				grpcKey = strings.Replace(grpcKey, "colon-", ":", 1)
			}
			for _, value := range values {
				md.Append(grpcKey, value)
			}
		}
	}

	ctx = metadata.NewOutgoingContext(ctx, md)

	decoder := NewProtobufStreamDecoder(r.Body)
	encoder := NewProtobufStreamEncoder(w)

	for {
		msg, err := decoder.Decode()
		if err == io.EOF {
			break
		}

		if err != nil {
			log.Printf("Stream decode error: %v", err)
			break
		}

		if err := encoder.Encode(msg); err != nil {
			log.Printf("Stream encode error: %v", err)
			break
		}

		flusher.Flush()
	}
}

func Start(http_proxy_port int, grpc_service_address string, protoFiles []string) {
	// Register all loaded file descriptors and message types with the global registries
	for _, protoPath := range protoFiles {
		fileBytes, err := os.ReadFile(protoPath)
		if err != nil {
			log.Fatalf("failed to read proto file %s: %v", protoPath, err)
		}
		var fds descriptorpb.FileDescriptorSet
		if err := proto.Unmarshal(fileBytes, &fds); err != nil {
			log.Fatalf("failed to unmarshal FileDescriptorSet from %s: %v", protoPath, err)
		}
		files, err := protodesc.NewFiles(&fds)
		if err != nil {
			log.Fatalf("failed to create protodesc.Files from %s: %v", protoPath, err)
		}
		files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
			_ = protoregistry.GlobalFiles.RegisterFile(fd)
			for i := 0; i < fd.Messages().Len(); i++ {
				md := fd.Messages().Get(i)
				_ = protoregistry.GlobalTypes.RegisterMessage(dynamicpb.NewMessageType(md))
			}
			return true
		})
	}

	httpToGRPC, err := NewHTTPToGRPCConverter(grpc_service_address)
	if err != nil {
		log.Fatalf("Failed to create HTTP to gRPC converter: %v", err)
	}
	defer httpToGRPC.grpcConn.Close()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Log method and path
		log.Printf("Incoming request: %s %s", r.Method, r.URL.Path)
		// Log headers
		for k, v := range r.Header {
			log.Printf("Header: %s: %v", k, v)
		}
		// Log body
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("Error reading body: %v", err)
		} else {
			log.Printf("Body length: %d", len(bodyBytes))
		}
		// Restore body for downstream handler
		r.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
		httpToGRPC.HTTPHandler()(w, r)
	})
	log.Printf("Starting HTTP server on:%d", http_proxy_port)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", http_proxy_port), nil); err != nil {
		log.Fatalf("Failed to start HTTP server: %v", err)
	}
}
