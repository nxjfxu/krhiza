package client

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strconv"
	"sync"
	"time"

	"encoding/gob"

	"golang.org/x/crypto/blake2b"

	pb "github.com/nxjfxu/krhiza/internal/rpc/krhiza"
	"google.golang.org/grpc"

	"github.com/nxjfxu/krhiza/internal/keys"
)

type Message interface {
	GetSender() *[]byte
	GetSeq() int
}

func resultToMessage(r *pb.RecvResult) Message {
	var buffer bytes.Buffer

	switch r := r.Result.(type) {
	case *pb.RecvResult_Message:
		buffer.Write(r.Message.GetBody())
	case *pb.RecvResult_Failed:
		return nil
	}

	var m Message
	dec := gob.NewDecoder(&buffer)
	err := dec.Decode(&m)
	if err != nil {
		log.Fatalln("Recv failed: malformed response.")
	}
	return m
}

type TextMessage struct {
	Sender []byte
	Seq    int

	Text string
}

func (m TextMessage) GetSender() *[]byte { return &m.Sender }
func (m TextMessage) GetSeq() int        { return m.Seq }
func (m TextMessage) GetText() string    { return m.Text }

type AckMessage struct {
	Sender []byte
	Seq    int
}

func (m AckMessage) GetSender() *[]byte { return &m.Sender }
func (m AckMessage) GetSeq() int        { return m.Seq }

func NextKey(shared int, seq int) [keys.KeySize]byte {
	hash, err := blake2b.New(keys.KeySize, []byte{})
	if err != nil {
		log.Fatalln("key generation failed.  why?")
	}
	hash.Write([]byte(strconv.Itoa(shared)))
	hash.Write([]byte(strconv.Itoa(seq)))
	sum := hash.Sum([]byte{})
	if len(sum) != keys.KeySize {
		log.Fatal("???")
	}
	var result [keys.KeySize]byte
	copy(result[:], sum[:])
	return result
}

func init() {
	gob.Register(TextMessage{})
	gob.Register(AckMessage{})
}

type Client struct {
	lock        sync.RWMutex
	addresses   []string // List of available server addresses
	nextAddress int      // Index of next address to try

	seq        int
	sendShared int
	recvShared int

	retry int
}

func InitClient(addresses []string, seq int, sendShared int, recvShared int) *Client {
	return &Client{
		lock: sync.RWMutex{},

		addresses:   addresses,
		nextAddress: 0,

		seq:        seq,
		sendShared: sendShared,
		recvShared: recvShared,

		retry: 5,
	}
}

func (c *Client) GetSeq() int {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.seq
}

func (c *Client) IncrementSeq() {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.seq++
}

// Get one available client
func (c *Client) getClient() (pb.KRhizaClient, func(), error) {
	visited := -1
	for {
		if len(c.addresses) == 0 {
			return nil, nil, errors.New("no server address provided.")
		}

		if visited == c.nextAddress {
			return nil, nil, errors.New("failed to connect to any server.")
		}

		if visited == -1 {
			visited = c.nextAddress
		}
		addr := c.addresses[c.nextAddress]
		c.nextAddress++
		if c.nextAddress >= len(c.addresses) {
			c.nextAddress = 0
		}

		conn, err := grpc.Dial(addr,
			grpc.WithTimeout(time.Second*10),
			grpc.WithInsecure(),
			grpc.WithBlock())
		if err == nil {
			return pb.NewKRhizaClient(conn),
				func() { conn.Close() },
				nil
		}
	}
}

func (c *Client) Recv() (Message, error) {
	r, err := c.RecvBack(0)
	return r, err
}

func (c *Client) RecvBack(back int) (Message, error) {
	var fe error
	var r *pb.RecvResult
	for rt := 0; rt <= c.retry; rt++ {
		client, cc, err := c.getClient()
		if cc != nil {
			defer cc()
		}
		if err != nil {
			return nil, err
		}

		ctx, ctxc := context.WithTimeout(context.Background(), time.Second*20)
		defer ctxc()

		key := NextKey(c.recvShared, c.GetSeq()-back)
		r, err = client.Recv(ctx, &pb.Index{Index: key[:]})
		fe = err
		if err == nil {
			m := resultToMessage(r)
			if m == nil {
				return nil, nil
			}

			switch m.(type) {
			case TextMessage:
				defer c.Ack()
			case AckMessage:
			}

			return m, nil
		}
	}

	return nil, fe

	//return nil, errors.New("Recv failed after " + strconv.Itoa(c.retry) + "attempts.")
}

func (c *Client) Ack() error {
	for rt := 0; rt <= c.retry; rt++ {
		client, cc, err := c.getClient()
		if cc != nil {
			defer cc()
		}
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*20)
		defer cancel()

		var m Message = &AckMessage{make([]byte, 0), c.GetSeq()}

		var buffer bytes.Buffer
		enc := gob.NewEncoder(&buffer)
		err = enc.Encode(&m)
		if err != nil {
			return err
		}

		key := NextKey(c.sendShared, c.GetSeq())
		message := &pb.Message{
			Index: key[:],
			Body:  buffer.Bytes(),
		}

		_, err = client.Send(ctx, message)
		if err == nil {
			c.IncrementSeq()
			return nil
		}
	}

	return errors.New("Recv failed after " + strconv.Itoa(c.retry) + "attempts.")
}

func (c *Client) SendAndFinish(text string) error {
	var fe error
	for rt := 0; rt <= c.retry; rt++ {
		client, cc, err := c.getClient()
		if cc != nil {
			defer cc()
		}
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*20)
		defer cancel()

		var m Message = &TextMessage{make([]byte, 0), c.GetSeq(), text}

		var buffer bytes.Buffer
		enc := gob.NewEncoder(&buffer)
		err = enc.Encode(&m)
		if err != nil {
			return err
		}

		key := NextKey(c.sendShared, c.GetSeq())
		message := &pb.Message{
			Index: key[:],
			Body:  buffer.Bytes(),
		}

		_, err = client.Send(ctx, message)
		fe = err
		if err == nil {
			return nil
		}
	}

	return fe

	//	return errors.New("Recv failed after " + strconv.Itoa(c.retry) + "attempts.")
}

func (c *Client) Send(text string) error {
	err := c.SendAndFinish(text)
	if err != nil {
		return err
	}

	return c.WaitAck()
}

func (c *Client) RecvAck() (bool, error) {
	client, cc, err := c.getClient()
	if cc != nil {
		defer cc()
	}
	if err != nil {
		return false, err
	}

	ctx, ctxc := context.WithTimeout(context.Background(), time.Second*20)
	defer ctxc()

	key := NextKey(c.recvShared, c.GetSeq())
	r, err := client.Recv(ctx, &pb.Index{Index: key[:]})
	if err != nil {
		return false, nil
	}

	m := resultToMessage(r)
	if m == nil {
		return false, nil
	}

	switch m.(type) {
	case AckMessage:
		return true, nil
	default:
		return false, errors.New("Unexpected non-Ack message")
	}

}

func (c *Client) WaitAck() error {
	for i := 0; i < 10; i++ {
		time.Sleep(10 * time.Second)
		ok, _ := c.RecvAck()
		if ok {
			c.IncrementSeq()
			return nil
		}
	}
	return errors.New("Ack not Recv'd.")
}
