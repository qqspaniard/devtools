package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os/signal"
	"syscall"

	"github.com/interactionlabs/devtools/control-room/internal/broker"
	"github.com/interactionlabs/devtools/control-room/internal/statedir"
	"github.com/interactionlabs/devtools/control-room/internal/store"
	"github.com/interactionlabs/devtools/control-room/internal/web"
)

// cmdServe runs the long-lived broker: it opens the store, binds the control
// Unix socket and the loopback web server, wires the web server as the broker's
// bootstrap issuer, and serves until interrupted (SIGINT/SIGTERM), then shuts
// down cleanly. All diagnostics go to stderr; serve produces no machine-
// readable stdout output.
func cmdServe(args []string, _ io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "override state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	paths, err := statedir.Resolve(*stateDir)
	if err != nil {
		return err
	}
	if err := paths.Ensure(); err != nil {
		return err
	}

	st, err := store.Open(store.Options{Path: paths.DB})
	if err != nil {
		return err
	}
	defer st.Close()

	secret, err := broker.LoadOrCreateSecret(paths.Secret)
	if err != nil {
		return err
	}

	b, err := broker.New(broker.Config{Paths: paths, Store: st, Secret: secret})
	if err != nil {
		return err
	}
	if err := b.Listen(); err != nil {
		return err
	}

	// Loopback web server shares the store so browser decisions are immediately
	// durable and visible to agent polls.
	wsrv := web.NewServer(st, nil)
	if err := wsrv.Listen(); err != nil {
		return err
	}
	b.SetWebBaseURL(wsrv.BaseURL())
	b.SetBootstrapIssuer(wsrv)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(stderr, "control-room broker listening\n  socket: %s\n  review base: %s\n  state dir: %s\n",
		paths.Socket, wsrv.BaseURL(), paths.Dir)

	errCh := make(chan error, 2)
	go func() { errCh <- b.Serve(ctx) }()
	go func() { errCh <- wsrv.Serve(ctx) }()

	// Wait for shutdown signal, then both servers unwind on ctx cancellation.
	<-ctx.Done()
	fmt.Fprintln(stderr, "control-room: shutting down")

	// Drain both server goroutines.
	var firstErr error
	for i := 0; i < 2; i++ {
		if e := <-errCh; e != nil && firstErr == nil {
			firstErr = e
		}
	}
	return firstErr
}
