// Copyright 2026 Query Farm LLC - https://query.farm

// Command mockserver runs a standalone HTTP server exposing the tiny mock OData
// v4 service from internal/odatamock. It backs the haybarn SQL end-to-end tests:
// the Makefile starts it on a free port, reads the printed PORT line, and points
// the worker's odata functions at it via VGI_ODATA_TEST_URL.
//
// Usage:
//
//	mockserver [--addr 127.0.0.1:0]
//
// On startup it prints "PORT:<n>" (the bound TCP port) to stdout so a caller can
// discover the port even when binding to :0. It shuts down gracefully on
// SIGINT/SIGTERM so the Makefile's `kill` produces a clean exit.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Query-farm/vgi-odata/internal/odatamock"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "TCP address to listen on (host:port; port 0 = pick a free port)")
	// baseURL, when non-empty, is the absolute prefix used to build the
	// @odata.nextLink the mock advertises for page 2. It defaults to
	// http://127.0.0.1:<bound-port> (correct when the client shares the mock's
	// loopback, as in `make test-sql`), but the paging worker follows the
	// nextLink verbatim, so when the worker runs in a container the mock must
	// advertise an address the container can reach (e.g. a docker-network
	// hostname like http://odata-mock:8000). Set --base-url for that case.
	baseURLFlag := flag.String("base-url", "", "absolute scheme://host[:port] used to build the @odata.nextLink (default: http://127.0.0.1:<bound-port>)")
	flag.Parse()

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("mockserver: listen %q: %v", *addr, err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if *baseURLFlag != "" {
		baseURL = *baseURLFlag
	}

	fmt.Printf("PORT:%d\n", port)
	_ = os.Stdout.Sync()

	mock := odatamock.New()
	srv := &http.Server{Handler: mock.Handler(baseURL)}

	// Graceful shutdown on SIGINT/SIGTERM so the Makefile's `kill` is a clean
	// (exit 0) stop rather than a signalled termination.
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		<-ch
		_ = srv.Shutdown(context.Background())
	}()

	if err := srv.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("mockserver: serve: %v", err)
	}
}
