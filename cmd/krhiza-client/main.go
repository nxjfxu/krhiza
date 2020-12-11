package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/nxjfxu/krhiza/internal/greeting"
	"github.com/nxjfxu/krhiza/pkg/client"
)

var shouldSend = flag.Bool("send", false, "Send a message")
var shouldRecv = flag.Bool("recv", false, "Receive a message")
var seq = flag.Int("seq", 1, "The sequence number")
var sendShared = flag.Int("send-key", 0, "The shared secret for Sending")
var recvShared = flag.Int("recv-key", 0, "The shared secret for Recv'ing")

var addresses = flag.String(
	"addresses",
	"",
	"The nodes to connect to, separated by '/'s.")

func main() {
	flag.Parse()

	if *shouldSend && *shouldRecv {
		log.Fatalln("cannot send and recv at the same time.")
	}

	addressList := strings.Split(*addresses, "/")
	if len(addressList) == 1 && len(addressList[0]) == 0 {
		log.Fatalln("no address to connect to.")
	}

	c := client.InitClient(
		addressList,
		*seq,
		*sendShared,
		*recvShared)

	var err error
	if *shouldSend {
		t := greeting.Greeting(5)
		err = c.Send(t)
		if err == nil {
			fmt.Println("Send'd:", t)
		}
	} else if *shouldRecv {
		var m client.Message
		m, err = c.Recv()
		if err == nil {
			if m == nil {
				fmt.Println("Recv failed: not found.")
			} else {
				switch m := m.(type) {
				case client.TextMessage:
					fmt.Println("Recv'd Message.  Seq:", m.GetSeq())
					fmt.Println("----------")
					fmt.Println(m.GetText())
				case client.AckMessage:
					fmt.Println("Recv'd Ack.  Seq:", m.GetSeq())
				}
			}
		}
	} else {
		fmt.Println("no operation specified.  exiting...")
	}

	if err != nil {
		fmt.Println("error:", err)
	}
}
