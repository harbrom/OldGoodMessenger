package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"ogm.local/firstcontact/internal/testclient"
	"ogm.local/firstcontact/internal/wire"
)

func main() {
	address := flag.String("server", "127.0.0.1:19090", "local test relay address")
	id := flag.Uint("id", uint(wire.FirstID), "disposable test ID: 100001 or 100002")
	flag.Parse()
	if *id != uint(wire.FirstID) && *id != uint(wire.SecondID) {
		fmt.Fprintln(os.Stderr, "invalid test identity")
		os.Exit(1)
	}
	var client *testclient.Client
	var readerDone chan struct{}
	disconnect := func() {
		if client != nil {
			client.Close()
			<-readerDone
			client = nil
		}
	}
	connect := func() {
		disconnect()
		c, err := testclient.Dial(*address, uint32(*id))
		if err != nil {
			fmt.Println("Connect failed:", err)
			return
		}
		client = c
		readerDone = make(chan struct{})
		fmt.Println("Connected as", *id)
		go func(done chan struct{}) {
			defer close(done)
			for {
				f, err := c.Receive()
				if err != nil {
					fmt.Println("Disconnected:", err)
					return
				}
				switch f.Kind {
				case wire.Message:
					fmt.Printf("From %d: %s\n", f.ID, f.Text)
				case wire.Failure:
					fmt.Printf("Relay error %d (no delivery confirmation)\n", f.Code)
				default:
					fmt.Println("Unexpected server frame")
					c.Close()
					return
				}
			}
		}(readerDone)
	}
	connect()
	defer disconnect()
	fmt.Println("Commands: /send <100001|100002> <text>, /disconnect, /connect, /quit")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 4096)
	for scanner.Scan() {
		line := scanner.Text()
		switch line {
		case "/quit":
			return
		case "/disconnect":
			disconnect()
		case "/connect":
			connect()
		default:
			parts := strings.SplitN(line, " ", 3)
			if len(parts) != 3 || parts[0] != "/send" {
				fmt.Println("Use /send <id> <text>")
				continue
			}
			to, err := strconv.ParseUint(parts[1], 10, 32)
			if err != nil || client == nil {
				fmt.Println("Invalid recipient or disconnected")
				continue
			}
			if err = client.Send(uint32(to), parts[2]); err != nil {
				fmt.Println("Send failed:", err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Println("Input error:", err)
	}
}
