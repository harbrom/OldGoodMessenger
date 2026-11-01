// ogm-local is an experimental, trusted-context loopback harness, not a server.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(args []string, input io.Reader, output, diagnostics io.Writer) (err error) {
	flags := flag.NewFlagSet("ogm-local", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	path := flags.String("db", "", "required local SQLite filesystem path (parent must exist)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *path == "" || flags.NArg() != 0 {
		return errors.New("usage: ogm-local -db <local SQLite file>")
	}
	c, err := openConsole(*path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, c.close(output)) }()
	fmt.Fprintln(output, "Experimental loopback tool: Alice=100001, Bob=100002; trusted identities injected locally, NO authentication.")
	fmt.Fprintln(output, "SQLite history persists; pending sends do NOT survive exit. Type help for commands.")
	for _, client := range c.clients {
		fmt.Fprintf(output, "%s (%d): test writer=%d, TCP %s -> %s\n", client.name, client.account, client.writer, client.peer.conn.LocalAddr(), client.peer.conn.RemoteAddr())
	}
	scanner := bufio.NewScanner(input)
	// A valid command is under 2048 bytes; reject unbounded input without storing it.
	scanner.Buffer(make([]byte, 4096), 4096)
	for {
		fmt.Fprint(output, "ogm-local> ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		if c.command(scanner.Text(), output) {
			return nil
		}
	}
}
