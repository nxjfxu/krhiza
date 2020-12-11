package server

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/url"
	"os"
	"sort"
	"sync"
	"time"

	pb "github.com/nxjfxu/krhiza/internal/rpc/kademlia"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"

	"github.com/nxjfxu/krhiza/internal/keys"
)

// All Kademlia RPC requests should contain source
type KademliaRequest interface {
	GetSource() *pb.Source
}

func (c *Contact) ToNodeInfo() *pb.NodeInfo {
	return &pb.NodeInfo{Id: c.Id[:], Address: c.Address}
}

type Value struct {
	body []byte

	expiration time.Time
}

type KademliaServer struct {
	pb.UnimplementedKademliaServer

	k     int
	alpha int

	Id keys.Key

	vLock sync.RWMutex
	kLock sync.RWMutex

	values      map[keys.Key]Value
	routingTree RoutingTree

	// Route to this server
	address string
	port    string

	logger *log.Logger
}

// for making a new server

func InitKademliaServer(k int, alpha int, key *keys.Key, port string) *KademliaServer {
	// As the paper suggests, just use a random number for the server ID
	routingTree := &RoutingTreeLeaf{depth: 0, k: k}

	prefix := "KRS:" + keys.Format(key[:])[:8] + " "
	logger := log.New(os.Stderr, prefix, log.LstdFlags)

	return &KademliaServer{
		Id: *key,

		vLock: sync.RWMutex{},
		kLock: sync.RWMutex{},

		values:      make(map[keys.Key]Value),
		routingTree: routingTree,

		address: "127.0.0.1",
		port:    port,

		logger: logger,

		k:     k,
		alpha: alpha,
	}
}

// For KRhiza to call

func (s *KademliaServer) Initialize(addr string) {
	time.Sleep(100 * time.Millisecond)
	err := s.sendPing(addr, nil)
	if err != nil {
		// If ping fails then there is little point continuing
		s.logger.Fatalln("Ping failed:", err)
	}

	// Try lookup self
	s.lookup(s.k, &s.Id, false)
}

func (s *KademliaServer) CleanUp() {
	n := time.Now()

	s.vLock.Lock()
	defer s.vLock.Unlock()

	for k, v := range s.values {
		if n.After(v.expiration) {
			delete(s.values, k)
		}
	}
}

func (s *KademliaServer) Gossip() {
	key := keys.Random()
	s.lookup(s.k, &key, false)
}

func (s *KademliaServer) Send(key *keys.Key, message []byte) (int, error) {
	cs, _, err := s.lookup(s.k, key, false)
	if err != nil {
		return 0, err
	}

	ok := 0
	for _, c := range cs {
		err := s.sendStore(&c, key, message)
		if err == nil {
			ok++
		}
	}
	return ok, nil
}

func (s *KademliaServer) Recv(key *keys.Key) (*[]byte, error) {
	_, v, err := s.lookup(s.k, key, true)
	if err != nil {
		return nil, err
	}
	if v != nil {
		return &v, nil
	}
	return nil, nil
}

// For accessing the local messages to call

func (s *KademliaServer) setMessage(key keys.Key, message []byte) {
	s.vLock.Lock()
	defer s.vLock.Unlock()

	exp := time.Now().Add(time.Second * 3600)

	s.values[key] = Value{message, exp}
}

func (s *KademliaServer) getMessage(key keys.Key) *[]byte {
	s.vLock.RLock()
	defer s.vLock.RUnlock()

	if value, ok := s.values[key]; ok {
		return &value.body
	} else {
		return nil
	}
}

// Internal operations

// Kademlia RPCs to other nodes

func (s *KademliaServer) sendPing(addr string, key *keys.Key) error {
	if key != nil && *key == s.Id {
		return nil
	}

	conn, err := grpc.Dial(addr,
		grpc.WithTimeout(time.Second),
		grpc.WithInsecure(),
		grpc.WithBlock())

	if conn != nil {
		defer conn.Close()
	}

	if err != nil {
		s.logger.Fatalln("connection failed during Ping:", err)
		if key != nil {
			s.removeContact(key)
		}
		return err
	}

	c := pb.NewKademliaClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*500)
	defer cancel()

	m := &pb.PingMessage{
		Source: &pb.Source{
			Id:      s.Id[:],
			Address: s.address,
			Port:    s.port}}
	pc, err := c.Ping(ctx, m)

	if err != nil {
		if key != nil {
			s.removeContact(key)
		}
		return err
	}

	var id [keys.KeySize]byte
	copy(id[:], pc.GetSanity())

	s.addContact(&Contact{
		Id:      id,
		Address: addr})

	return nil
}

func (s *KademliaServer) sendStore(c *Contact, key *keys.Key, message []byte) error {
	if c.Id == s.Id {
		s.setMessage(*key, message)
		return nil
	}

	conn, err := grpc.Dial(c.Address,
		grpc.WithTimeout(time.Second),
		grpc.WithInsecure(),
		grpc.WithBlock())

	if conn != nil {
		defer conn.Close()
	}

	if err != nil {
		s.removeContact(&c.Id)
		return err
	}

	client := pb.NewKademliaClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*500)
	defer cancel()

	m := &pb.KeyValuePair{
		Source: &pb.Source{
			Id:      s.Id[:],
			Address: s.address,
			Port:    s.port},

		Key:   key[:],
		Value: message}

	_, err = client.Store(ctx, m)
	return err
}

func (s *KademliaServer) sendFindNode(c *Contact, key *keys.Key, value bool) ([]Contact, []byte, error) {
	if c.Id == s.Id {
		if value {
			m := s.getMessage(*key)
			if m != nil {
				return nil, *m, nil
			}
		}

		s.kLock.RLock()
		cds := ByDistance(s.routingTree.ClosestK(s.k, key))
		s.kLock.RUnlock()
		cs := make([]Contact, len(cds))
		for i, cd := range cds {
			cs[i] = cd.contact
		}
		return cs, nil, nil
	}

	conn, err := grpc.Dial(c.Address,
		grpc.WithTimeout(time.Second),
		grpc.WithInsecure(),
		grpc.WithBlock())

	if conn != nil {
		defer conn.Close()
	}

	if err != nil {
		s.removeContact(&c.Id)
		return nil, nil, err
	}

	client := pb.NewKademliaClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*500)
	defer cancel()

	m := &pb.Key{
		Source: &pb.Source{
			Id:      s.Id[:],
			Address: s.address,
			Port:    s.port},

		Key: key[:]}

	if value {
		result, err := client.FindValue(ctx, m)
		if err != nil {
			return nil, nil, err
		}
		switch r := result.Result.(type) {
		case *pb.FindValueResult_Value:
			return nil, r.Value, nil
		case *pb.FindValueResult_Nodes:
			nis := r.Nodes.Results
			cs := make([]Contact, len(nis))
			for i, ni := range nis {
				var id [keys.KeySize]byte
				copy(id[:], ni.GetId())
				cs[i] = Contact{id, ni.GetAddress()}
			}
			return cs, nil, nil
		default:
			return nil, nil, errors.New("wrong type")
		}
	} else {
		result, err := client.FindNode(ctx, m)
		nis := result.GetResults()

		cs := make([]Contact, len(nis))
		for i, ni := range nis {
			var id [keys.KeySize]byte
			copy(id[:], ni.GetId())
			cs[i] = Contact{id, ni.GetAddress()}
		}

		return cs, nil, err
	}
}

// Fully internal operations
func (s *KademliaServer) tryAddContact(ctx context.Context, in KademliaRequest) error {
	if bytes.Equal(in.GetSource().GetId(), s.Id[:]) {
		return nil
	} else {
		p, ok := peer.FromContext(ctx)
		if ok {
			uCtx, err := url.Parse("//" + p.Addr.String())
			if err != nil {
				log.Fatalln("WHAT???")
				return err
			}
			aIn := string(in.GetSource().GetAddress())

			uCtxHost := uCtx.Hostname()
			if uCtxHost != aIn {
				return errors.New("mismatch hostname...  what?")
			}

			u := uCtxHost + ":" + in.GetSource().GetPort()

			var id keys.Key
			copy(id[:], in.GetSource().GetId())
			s.addContact(&Contact{id, u})
		}
	}

	return nil
}

func (s *KademliaServer) addContactAfter(c *Contact, d time.Duration) {
	time.Sleep(d)
	s.addContact(c)
}

func (s *KademliaServer) addContact(c *Contact) {
	if c.Id == s.Id {
		return
	}

	change := false
	s.kLock.Lock()
	s.routingTree, change = s.routingTree.AddContact(&s.Id, c)
	s.kLock.Unlock()

	if !change {
		return
	}

	// if changed, consider republish if the new contact is closer
	s.vLock.RLock()
	for k, v := range s.values {
		if keys.Le(keys.Distance(&c.Id, &k), keys.Distance(&s.Id, &k)) {
			defer s.sendStore(c, &k, v.body)
		}
	}
	s.vLock.RUnlock()
}

func (s *KademliaServer) removeContact(key *keys.Key) {
	if *key == s.Id {
		return
	}

	s.kLock.Lock()
	defer s.kLock.Unlock()

	s.routingTree = s.routingTree.RemoveContact(key)
}

func (s *KademliaServer) lookup(k int, key *keys.Key, value bool) ([]Contact, []byte, error) {
	if value {
		m := s.getMessage(*key)
		if m != nil {
			return nil, *m, nil
		}
	}

	scd := (&Contact{s.Id, s.address + ":" + s.port}).Distance(key)

	s.kLock.RLock()
	cds := append(s.routingTree.ClosestK(k, key), scd)
	s.kLock.RUnlock()

	// The first should be the closest
	sort.Sort(ByCloseness(cds))

	if len(cds) > k {
		cds = cds[:k]
	}

	visited := map[keys.Key]bool{
		s.Id: true,
	}

	added := make(map[keys.Key]bool)
	for _, cd := range cds {
		added[cd.contact.Id] = true
	}

	finished := false
	for !finished {
		finished = true

		count := 0
		alpha := s.alpha
		if k < alpha {
			alpha = k
		}
		ccs := make(chan *[]Contact, alpha)
		cv := make(chan *[]byte, alpha)

		for _, cd := range cds {
			if count >= alpha {
				break
			}

			if visited[cd.contact.Id] {
				continue
			} else {
				visited[cd.contact.Id] = true
				finished = false
				count++
			}
			go func() {

				cs, v, err := s.sendFindNode(&cd.contact, key, value)
				if err != nil {
					ccs <- nil
				} else if value && v != nil {
					cv <- &v
				} else {
					ccs <- &cs
				}
			}()
		}

		for i := 0; i < count; i++ {
			select {
			case cs := <-ccs:
				if cs != nil {
					// Add contact!
					for _, c := range *cs {
						if added[c.Id] {
							continue
						}

						added[c.Id] = true
						go s.addContact(&c)

						contained := visited[c.Id]
						for _, cd := range cds {
							if cd.contact.Id == c.Id {
								contained = true
								break
							}
						}
						if !contained {
							cds = append(cds, c.Distance(key))
						}
					}
				}
			case v := <-cv:
				if value && v != nil {
					return nil, *v, nil
				}
			}
		}

		sort.Stable(ByCloseness(cds))
		if len(cds) > k {
			cds = cds[:k]
		}
	}

	results := make([]Contact, len(cds))
	for i, cd := range cds {
		results[i] = cd.contact
	}
	if len(cds) > k {
		log.Fatalf("internal problems.")
	}

	return results, nil, nil
}

// Kademlia Protocol implementations

func (s *KademliaServer) Ping(ctx context.Context, in *pb.PingMessage) (*pb.PingConfirm, error) {
	go s.tryAddContact(ctx, in)

	return &pb.PingConfirm{Sanity: s.Id[:]}, nil
}

func (s *KademliaServer) Store(ctx context.Context, in *pb.KeyValuePair) (*pb.StoreConfirm, error) {
	key := in.Key
	var messageKey keys.Key
	copy(messageKey[:], key[:])

	s.setMessage(messageKey, in.GetValue())

	go s.tryAddContact(ctx, in)

	return &pb.StoreConfirm{Sanity: s.Id[:]}, nil
}

func (s *KademliaServer) FindNode(ctx context.Context, in *pb.Key) (*pb.FindNodeResult, error) {
	go s.tryAddContact(ctx, in)

	var key keys.Key
	copy(key[:], in.GetKey())

	s.kLock.RLock()
	cds := s.routingTree.ClosestK(s.k, &key)
	s.kLock.RUnlock()

	results := make([]*pb.NodeInfo, len(cds))
	for i, cd := range cds {
		c := cd.contact
		results[i] = &pb.NodeInfo{Id: c.Id[:], Address: c.Address}
	}

	return &pb.FindNodeResult{Sanity: s.Id[:],
		Results: results}, nil
}

func (s *KademliaServer) FindValue(ctx context.Context, in *pb.Key) (*pb.FindValueResult, error) {
	var key keys.Key
	copy(key[:], in.GetKey()[:])

	value := s.getMessage(key)
	if value != nil {
		// If we have the value then return otherwise fail for the time being
		return &pb.FindValueResult{Sanity: s.Id[:],
			Result: &pb.FindValueResult_Value{[]byte(*value)}}, nil
	} else {
		result, err := s.FindNode(ctx, in)
		// Do not try to add node here because it is done in FindNode anyway
		if err != nil {
			return nil, err
		} else {
			return &pb.FindValueResult{Sanity: result.Sanity,
				Result: &pb.FindValueResult_Nodes{result}}, err
		}
	}
}
