package continuous

import (
	"context"
	"net/http"

	"google.golang.org/grpc"
)

type httpServer struct {
	*http.Server
}

func (s *httpServer) Stop() error {
	return s.Server.Close()
}
func (s *httpServer) GracefulStop() error {
	return s.Server.Shutdown(context.Background())
}

func WrapHTTPServer(s *http.Server) Continuous {
	return &httpServer{s}
}

type grpcServer struct {
	*grpc.Server
}

func (s *grpcServer) Stop() error {
	s.Server.Stop()
	return nil
}
func (s *grpcServer) GracefulStop() error {
	s.Server.GracefulStop()
	return nil
}

func WrapGRPCServer(s *grpc.Server) Continuous {
	return &grpcServer{s}
}
