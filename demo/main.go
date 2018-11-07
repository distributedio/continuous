package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/shafreeck/continuous"
	"google.golang.org/grpc"
	pb "google.golang.org/grpc/examples/helloworld/helloworld"
)

// httpServer implements the Continuous interface
type httpServer struct {
	*http.Server
}

func (hs *httpServer) Stop() error {
	return hs.Close()
}
func (hs *httpServer) GracefulStop() error {
	return hs.Shutdown(context.Background())
}

// http handler
type httpd struct{}

func (h *httpd) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(200)
	w.Write([]byte("You got me!"))
}

// grpcServer implements the Continuous interface
type grpcServer struct {
	*grpc.Server
}

func (gs *grpcServer) Stop() error {
	gs.Server.Stop()
	return nil
}
func (gs *grpcServer) GracefulStop() error {
	gs.Server.GracefulStop()
	return nil
}

// grpc handler
type helloServer struct{}

func (hs *helloServer) SayHello(ctx context.Context, req *pb.HelloRequest) (*pb.HelloReply, error) {
	reply := &pb.HelloReply{}
	reply.Message = "hello " + req.Name
	return reply, nil
}

func main() {
	cont := continuous.New()

	// srv1 implements Continuous
	srv1 := &httpServer{Server: &http.Server{Handler: &httpd{}}}
	cont.AddServer(srv1, &continuous.ListenOn{"tcp", ":8000"})
	cont.AddServer(srv1, &continuous.ListenOn{"tcp", ":8001"})
	cont.AddServer(srv1, &continuous.ListenOn{"tcp", ":8002"})

	// srv2 implements Continuous
	srv2 := &grpcServer{Server: grpc.NewServer()}
	pb.RegisterGreeterServer(srv2.Server, &helloServer{})
	cont.AddServer(srv2, &continuous.ListenOn{"tcp", ":50051"})
	cont.AddServer(srv2, &continuous.ListenOn{"tcp", ":50052"})
	cont.AddServer(srv2, &continuous.ListenOn{"tcp", ":50053"})

	if err := cont.Serve(); err != nil {
		fmt.Println(err)
	}
}
