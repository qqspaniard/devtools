package broker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/interactionlabs/devtools/control-room/internal/protocol"
	"github.com/interactionlabs/devtools/control-room/internal/statedir"
	"github.com/interactionlabs/devtools/control-room/internal/store"
)

// connReadTimeout bounds how long a single control request may take to arrive
// once a connection is accepted, so a slow or stuck client cannot pin a
// handler goroutine indefinitely.
const connReadTimeout = 30 * time.Second

// Broker is the long-lived local process. It owns the store, the Unix socket
// listener, and (optionally) the loopback web server. It is created with New
// and driven by Serve.
type Broker struct {
	paths  statedir.Paths
	secret string
	store  *store.Store

	// webBaseURL is the base URL of the loopback web server, set once the web
	// server starts, used to build session review URLs returned to clients.
	mu         sync.RWMutex
	webBaseURL string

	ln net.Listener
}

// Config configures a Broker.
type Config struct {
	Paths statedir.Paths
	Store *store.Store
	// Secret is the control secret every request must present.
	Secret string
}

// New constructs a Broker. The store must already be open.
func New(cfg Config) (*Broker, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("broker: New requires a store")
	}
	if cfg.Secret == "" {
		return nil, fmt.Errorf("broker: New requires a control secret")
	}
	return &Broker{
		paths:  cfg.Paths,
		secret: cfg.Secret,
		store:  cfg.Store,
	}, nil
}

// SetWebBaseURL records the loopback web base URL so review URLs can be built.
func (b *Broker) SetWebBaseURL(u string) {
	b.mu.Lock()
	b.webBaseURL = u
	b.mu.Unlock()
}

func (b *Broker) webBase() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.webBaseURL
}

// Store exposes the underlying store (used by the web server, which shares it).
func (b *Broker) Store() *store.Store { return b.store }

// maxSocketPathLen bounds the Unix socket path. The kernel's sockaddr_un.sun_path
// is small (104 bytes on darwin, 108 on Linux); a path at or above the limit is
// silently truncated or rejected with an opaque EINVAL. We fail closed with an
// actionable diagnostic instead. The RFC default path is well within this; only
// a pathologically deep custom --state-dir can trip it.
const maxSocketPathLen = 103

// Listen binds the Unix domain socket, handling a stale socket left by a
// previous crash WITHOUT trusting any PID file. Ownership is established by the
// socket itself: if we can connect to an existing socket the broker is already
// running (caller should reuse it); if connecting fails, the socket is stale
// and safe to remove and rebind.
func (b *Broker) Listen() error {
	if len(b.paths.Socket) > maxSocketPathLen {
		return fmt.Errorf(
			"broker: socket path %q is %d bytes, exceeding the OS limit of %d; choose a shorter --state-dir",
			b.paths.Socket, len(b.paths.Socket), maxSocketPathLen)
	}
	if _, err := os.Stat(b.paths.Socket); err == nil {
		// A socket file exists. Probe it: a live broker answers.
		c, derr := net.DialTimeout("unix", b.paths.Socket, 500*time.Millisecond)
		if derr == nil {
			_ = c.Close()
			return fmt.Errorf("broker: a broker is already listening on %s", b.paths.Socket)
		}
		// Stale socket (connection refused / no listener). Remove it.
		if rmErr := os.Remove(b.paths.Socket); rmErr != nil && !os.IsNotExist(rmErr) {
			return fmt.Errorf("broker: removing stale socket %s: %w", b.paths.Socket, rmErr)
		}
	}
	ln, err := net.Listen("unix", b.paths.Socket)
	if err != nil {
		return fmt.Errorf("broker: listening on %s: %w", b.paths.Socket, err)
	}
	// The socket lives inside the 0700 state dir, so it is already unreachable
	// by other users; tighten its own mode too as belt-and-braces.
	if err := os.Chmod(b.paths.Socket, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("broker: securing socket: %w", err)
	}
	b.ln = ln
	return nil
}

// Serve accepts connections until ctx is cancelled. On cancellation it closes
// the listener and removes the socket file for a clean shutdown. It blocks
// until the accept loop exits.
func (b *Broker) Serve(ctx context.Context) error {
	if b.ln == nil {
		return fmt.Errorf("broker: Serve called before Listen")
	}
	var wg sync.WaitGroup

	// Close the listener when the context is done so Accept unblocks.
	go func() {
		<-ctx.Done()
		_ = b.ln.Close()
	}()

	for {
		conn, err := b.ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				break // clean shutdown
			}
			if errors.Is(err, net.ErrClosed) {
				break
			}
			// Transient accept error: keep serving.
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.handleConn(conn)
		}()
	}

	wg.Wait()
	// Best-effort socket cleanup so the next start sees no stale file.
	_ = os.Remove(b.paths.Socket)
	return nil
}

// handleConn serves exactly one request per connection: read one framed
// request, dispatch it, write one framed response, close. A one-shot connection
// model keeps lifecycle trivial and removes any need for per-connection state.
func (b *Broker) handleConn(conn net.Conn) {
	defer conn.Close()

	// Transport-level defense in depth: refuse peers running as a different OS
	// user before reading any request bytes.
	if err := checkPeerUID(conn); err != nil {
		_ = writeResponse(conn, errResponse(CodeUnauthorized, err.Error()))
		return
	}

	_ = conn.SetReadDeadline(time.Now().Add(connReadTimeout))
	payload, err := protocol.ReadFrame(conn)
	if err != nil {
		if err == io.EOF {
			return // client hung up without sending
		}
		_ = writeResponse(conn, errResponse(CodeBadRequest, err.Error()))
		return
	}

	resp := b.dispatch(payload)
	_ = writeResponse(conn, resp)
}

// writeResponse marshals and frames a response.
func writeResponse(conn net.Conn, resp Response) error {
	raw, err := marshalResponse(resp)
	if err != nil {
		return err
	}
	return protocol.WriteFrame(conn, raw)
}
