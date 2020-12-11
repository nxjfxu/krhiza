package main

import (
	"context"
	crand "crypto/rand"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"time"

	kpb "github.com/nxjfxu/krhiza/internal/rpc/kademlia"
	krpb "github.com/nxjfxu/krhiza/internal/rpc/krhiza"
	"google.golang.org/grpc"

	"github.com/nxjfxu/krhiza/internal/keys"
	server "github.com/nxjfxu/krhiza/pkg/server"
)

var port = flag.String("port", "60001", "The port to listen to.")
var initAddr = flag.String("init", "", "The node to connect to during initialization.")
var k = flag.Int("k", 10, "The replication factor.")
var alpha = flag.Int("alpha", 1, "The concurrency factor.")

func address() string {
	return "127.0.0.1:" + *port
}

type KRhizaServer struct {
	krpb.UnimplementedKRhizaServer

	kademlia kpb.KademliaClient
}

func (s *KRhizaServer) Confirm(ctx context.Context, in *krpb.ClientInfo) (*krpb.ServerInfo, error) {
	return &krpb.ServerInfo{Name: fmt.Sprintf("S[%d]", os.Getpid())}, nil
}

func (s *KRhizaServer) Send(ctx context.Context, in *krpb.Message) (*krpb.SendResult, error) {
	var key keys.Key
	copy(key[:], in.GetIndex())
	replicas, err := kademlia.Send(&key, in.GetBody())
	if err != nil {
		return nil, err
	} else {
		return &krpb.SendResult{Sent: int32(replicas)}, nil
	}
}

func (s *KRhizaServer) Recv(ctx context.Context, in *krpb.Index) (*krpb.RecvResult, error) {
	var key keys.Key
	copy(key[:], in.GetIndex())
	message, err := kademlia.Recv(&key)

	if err != nil {
		return nil, err
	}
	if message != nil {
		m := &krpb.Message{Index: in.GetIndex(), Body: *message}
		return &krpb.RecvResult{Result: &krpb.RecvResult_Message{m}},
			nil
	} else {
		return &krpb.RecvResult{Result: &krpb.RecvResult_Failed{}},
			nil
	}
}

var kademlia *server.KademliaServer
var kademliaKey keys.Key

func init() {
	var seed [2]byte
	crand.Read(seed[:])
	rand.Seed(int64(seed[0])<<8 | int64(seed[1]))
	kademliaKey = keys.Random()
}

func main() {
	flag.Parse()

	// Kademlia
	kademlia = server.InitKademliaServer(*k, *alpha, &kademliaKey, *port)

	lis, err := net.Listen("tcp", ":"+*port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	s := grpc.NewServer()

	// Display information about this server
	kpb.RegisterKademliaServer(s, kademlia)

	// KRhiza
	krhiza := &KRhizaServer{}
	krpb.RegisterKRhizaServer(s, krhiza)

	if len(*initAddr) > 0 {
		go kademlia.Initialize(*initAddr)
	} else {
		log.Println("no initialization address provided.  Skipping initialization")
	}

	go gossip()
	go cleanUp()

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}

	defer s.Stop()
}

func cleanUp() {
	for {
		time.Sleep(15 * time.Second)
		kademlia.CleanUp()
	}
}

func gossip() {
	for {
		time.Sleep(30 * time.Second)
		time.Sleep(time.Duration(rand.Int()%10000) * time.Millisecond)
		kademlia.Gossip()
	}
}
