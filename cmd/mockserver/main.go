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
	flag.Parse()

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("mockserver: listen %q: %v", *addr, err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

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
