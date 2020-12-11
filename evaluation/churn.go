// churn churn churn

package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"syscall" // ;-(

	"github.com/nxjfxu/krhiza/pkg/client"
)

// Server statistics
var nextNumber int32 = 50001
var churnAverage = 0.0
var lost int32 = 0

// Communication statstics
var communicationSuccess int32 = 0
var communicationFailed int32 = 0

var running map[int32]*exec.Cmd = make(map[int32]*exec.Cmd)
var stableRunning map[int32]bool = make(map[int32]bool)
var rLock = sync.RWMutex{}

var seed = flag.Int("seed", 0, "The pseudo-random seed for the simulation.")
var serverCount = flag.Int("count", 100, "The maximum number of concurrent servers.")
var runFor = flag.Int("run", 600, "The time to run, in seconds.")
var redirect = flag.Bool("redirect", false, "Redirect server output.")
var stableCount = flag.Int("stable", 0, "The number of stable servers.")

var hasStableNodes = false

// Fail factors
var minTryInterval = flag.Int("f-min", 5, "The minimum time to wait before next failure try.")
var randomTryInterval = flag.Int("f-rand", 5, "The randomized time period to wait before next failure try.")
var baseFailThreshold = flag.Float64("f-threshold", 0.95, "The initial possibility to survive a failure try.")
var dFailThreshold = flag.Float64("f-threshold-increase", 0.005, "The increase in survival possibility after each failure try.")
var maxFailThreshold = flag.Float64("f-threshold-max", 0.95, "The maximum possibility to survive a failure try.")
var stabilizationPeriod = flag.Int("stabilization", 30, "The length of the stablization failure.")
var stableFailThreshold = flag.Float64("f-threshold-stable", 0.999, "The possibility for a stable node to survive a failure try.")

var k = flag.Int("k", 10, "The replication factor.")

const (
	recvSuccess  = iota
	recvNotFound = iota
	recvGarbage  = iota
	recvFail     = iota
)

func getNextNumber() int32 {
	n := nextNumber
	atomic.AddInt32(&nextNumber, 1)
	return n
}

func getOneRunning() int32 {
	rLock.RLock()
	defer rLock.RUnlock()

	if len(running) == 0 {
		return -1
	}

	if hasStableNodes {
		chosen := rand.Int() % len(stableRunning)
		i := 0
		for p, _ := range stableRunning {
			if i == chosen {
				return p
			}
			i++
		}
	} else {
		chosen := rand.Int() % len(running)
		i := 0
		for p, _ := range running {
			if i == chosen {
				return p
			}
			i++
		}
	}

	return -1
}

func init() {
	rand.Seed(int64(*seed))

	log.SetFlags(log.Lmicroseconds)
	log.SetPrefix("churn: ")

	var rLimit syscall.Rlimit
	syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rLimit)
	log.Println("file number limit:", rLimit)
	rLimit.Cur *= 20
	syscall.Setrlimit(syscall.RLIMIT_NOFILE, &rLimit)
	log.Println("changed to:", rLimit)
}

func main() {
	flag.Parse()
	hasStableNodes = *stableCount > 0

	sigintChan := make(chan os.Signal, 1)
	signal.Notify(sigintChan, os.Interrupt)

	allUp := make(chan bool, 1)

	fc := make(chan bool)

	go logStatus()
	go spawnAll([]chan bool{allUp}, fc)
	go startCalculation(allUp)
	go runCommunications()
	defer finish()

	select {
	case <-fc:
		log.Println("finished after", *runFor, "seconds")
	case <-sigintChan:
		log.Println("finished after interruption")
	}
}

func spawnAll(allUps []chan bool, finish chan bool) {
	for i := 0; i < *serverCount; i++ {
		go run()
		time.Sleep(time.Millisecond * 200)
	}

	// Wait for a while
	time.Sleep(time.Second * time.Duration(*stabilizationPeriod))

	for _, c := range allUps {
		c <- true
	}

	log.Println("all servers spawned.  starting count down.")

	time.Sleep(time.Second * time.Duration(*runFor))
	finish <- true
}

func finish() {
	communicationCount := communicationFailed + communicationSuccess
	communicationFailRate := float64(communicationFailed) / float64(communicationCount)

	fmt.Println("----------")
	fmt.Println("S T A T S")
	fmt.Println("----------")
	fmt.Println("     average churn rate:", churnAverage*100.0, "%")
	fmt.Println("    communication count:", communicationCount)
	fmt.Println("communication fail rate:", communicationFailRate*100.0, "%")
	fmt.Println("----------")

	log.Println("shutting down.")
	count := 0
	for _, cmd := range running {
		if cmd.Process != nil {
			cmd.Process.Kill()
			count++
		}
	}
	log.Println("stopped", count, "server processes.")
}

func startCalculation(allUp chan bool) {
	<-allUp
	log.Println("start calculating failure rate.")
	atomic.StoreInt32(&communicationSuccess, 0)
	atomic.StoreInt32(&communicationFailed, 0)
	log.Println("start calculating churn rate.")

	atomic.StoreInt32(&lost, 0)
	for i := 0; ; i++ {
		rLock.RLock()
		count := len(running)
		rLock.RUnlock()

		time.Sleep(time.Second * 60)
		lost := atomic.SwapInt32(&lost, 0)

		rate := float64(lost) / float64(count)
		log.Println("churn rate in last 60 seconds:", rate*100.0, "%")
		churnAverage = (churnAverage*float64(i) + rate) / float64(i+1)
	}
}

func run() {
	for {
		n := getNextNumber()
		var cmd *exec.Cmd
		ping := getOneRunning()
		if ping == -1 {
			cmd = exec.Command(
				"krhiza-server",
				"-k", strconv.Itoa(*k),
				"-port", strconv.Itoa(int(n)))
		} else {
			//			ping = 40002
			cmd = exec.Command(
				"krhiza-server",
				"-k", strconv.Itoa(*k),
				"-port", strconv.Itoa(int(n)),
				"-init", "127.0.0.1:"+strconv.Itoa(int(ping)))
		}
		if *redirect {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		}
		cmd.Env = append(cmd.Env, "GOMAXPROCS=2")

		rLock.Lock()
		running[n] = cmd
		err := cmd.Start()
		if err != nil {
			log.Println("failed to start:", err)
		} else {
			//			log.Println("started at port:", n, " ping", ping)
		}

		stable := len(stableRunning) < *stableCount
		if stable {
			stableRunning[n] = true
		}

		rLock.Unlock()

		c := make(chan bool, 2)

		go func() {
			threshold := *baseFailThreshold
			if stable {
				threshold = *stableFailThreshold
			}

			// The more attempts a server survives, the less likely it failes
			for a := 0; ; a++ {
				time.Sleep(time.Second * time.Duration(*minTryInterval+rand.Int()%*randomTryInterval))
				if rand.Float64() > threshold {
					break
				}

				if threshold < *maxFailThreshold && !stable {
					threshold += *dFailThreshold
				}
			}
			c <- true
			cmd.Process.Kill()
			cmd.Process.Release()
		}()

		go func() {
			cmd.Wait()
			c <- true
			//			log.Println("server crashed before failure.  port:", n)
		}()

		<-c

		rLock.Lock()
		delete(running, n)
		if stableRunning[n] {
			delete(stableRunning, n)
		}
		rLock.Unlock()

		atomic.AddInt32(&lost, 1)

		//		log.Println("finished at port:", n)

		time.Sleep(time.Second * time.Duration(*minTryInterval+rand.Int()%*randomTryInterval))
	}
}

func logStatus() {
	log.Println("deployment started.")
	for {
		time.Sleep(time.Second * 10)
		rLock.RLock()
		communicationCount := communicationFailed + communicationSuccess
		communicationFailRate := float64(communicationFailed) / float64(communicationCount)
		log.Println(len(running), "running.")
		log.Println("     average churn rate:", churnAverage*100.0, "%")
		log.Println("    communication count:", communicationCount)
		log.Println("communication fail rate:", communicationFailRate*100.0, "%")
		rLock.RUnlock()
	}
}

// Client operations
func runCommunications() {
	time.Sleep(time.Second * 20)
	log.Println("start counting communications.")

	for i := 1; ; i++ {
		time.Sleep(time.Millisecond * 200)
		go communicate(i)
	}
}

func communicate(n int) {
	//	log.Println("communiate:", n)

	senderAddressList := make([]string, 5)
	for i := 0; i < 5; i++ {
		senderAddressList[i] = "127.0.0.1:" + strconv.Itoa(int(getOneRunning()))
	}

	secret1 := rand.Int()
	secret2 := secret1 + 1

	sender := client.InitClient(senderAddressList, 1, secret1, secret2)

	text := strconv.Itoa(rand.Int())

	sendC := make(chan bool, 1)
	recvC := make(chan int, 1)

	var recvErr error

	go func() {
		err := sender.Send(text)
		if err != nil {
			log.Println("SEND FAIL:", err)
		}
		sendC <- err == nil
	}()

	time.Sleep(time.Second * 30)
	receiverAddressList := make([]string, 5)
	for i := 0; i < 5; i++ {
		receiverAddressList[i] = "127.0.0.1:" + strconv.Itoa(int(getOneRunning()))
	}
	receiver := client.InitClient(receiverAddressList, 1, secret2, secret1)

	go func() {
		m, err := receiver.Recv()
		if err != nil {
			log.Println("RECV FAIL:", err)
			recvErr = err
			recvC <- recvFail
		} else if m == nil {
			log.Println("RECV NOT FOUND:")
			recvC <- recvNotFound
		} else {
			switch m := m.(type) {
			case client.TextMessage:
				if m.GetText() == text {
					recvC <- recvSuccess
				} else {
					recvC <- recvGarbage
				}
			default:
				recvC <- recvGarbage
			}
		}
	}()

	sendResult := <-sendC
	recvResult := <-recvC

	if sendResult && recvResult == recvSuccess {
		atomic.AddInt32(&communicationSuccess, 1)
	} else {
		atomic.AddInt32(&communicationFailed, 1)
	}
}
