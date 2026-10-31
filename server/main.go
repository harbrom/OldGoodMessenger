package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"

	"ogm.local/firstcontact/internal/relay"
)

func main() {
	address := flag.String("listen", "127.0.0.1:19090", "numeric loopback address only")
	flag.Parse()
	listener, err := relay.Listen(*address)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	log.Printf("First Contact experimental relay listening on %s", listener.Addr())
	if err = relay.New().Serve(ctx, listener); err != nil {
		log.Fatal(err)
	}
}
