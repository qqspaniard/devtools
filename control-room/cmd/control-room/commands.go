package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"

	"github.com/interactionlabs/devtools/control-room/internal/broker"
	"github.com/interactionlabs/devtools/control-room/internal/protocol"
	"github.com/interactionlabs/devtools/control-room/internal/statedir"
)

// readBoundedFile reads a plan file, bounded to MaxPlanBytes so a huge file
// fails with a clear error rather than an unbounded read.
func readBoundedFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening plan file: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, protocol.MaxPlanBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading plan file: %w", err)
	}
	if len(data) > protocol.MaxPlanBytes {
		return nil, fmt.Errorf("plan file exceeds %d bytes", protocol.MaxPlanBytes)
	}
	return data, nil
}

// dialBroker resolves the state dir and returns a client connected to the
// running broker's socket. It does NOT auto-spawn a broker: for this slice an
// explicit `serve` is required, keeping lifecycle simple and the journey
// predictable. A clear diagnostic is returned if no broker answers.
func dialBroker(stateDir string) (*broker.Client, statedir.Paths, error) {
	paths, err := statedir.Resolve(stateDir)
	if err != nil {
		return nil, statedir.Paths{}, err
	}
	secret, err := broker.LoadOrCreateSecret(paths.Secret)
	if err != nil {
		return nil, paths, fmt.Errorf("no control secret at %s (is the broker running? run `control-room serve`): %w", paths.Secret, err)
	}
	c := broker.NewClient(paths.Socket, secret)
	if err := c.Ping(); err != nil {
		return nil, paths, fmt.Errorf("cannot reach broker on %s (run `control-room serve`): %w", paths.Socket, err)
	}
	return c, paths, nil
}

// writeJSON emits a value as indented JSON to stdout — the machine-readable
// output an adapter consumes.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// --- session ---

func cmdSession(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("session: expected subcommand (create|get|end)")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "create":
		return cmdSessionCreate(rest, stdout, stderr)
	case "get":
		return cmdSessionGet(rest, stdout, stderr)
	case "end":
		return cmdSessionEnd(rest, stdout, stderr)
	default:
		return fmt.Errorf("session: unknown subcommand %q", sub)
	}
}

func cmdSessionCreate(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("session create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "override state directory")
	workspaceID := fs.String("workspace-id", "", "workspace identity (required)")
	workspaceName := fs.String("workspace-name", "", "workspace display name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *workspaceID == "" {
		return fmt.Errorf("session create: --workspace-id is required")
	}
	c, _, err := dialBroker(*stateDir)
	if err != nil {
		return err
	}
	sess, err := c.SessionCreate(*workspaceID, *workspaceName)
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "created session %s (state=%s)\n", sess.ID, sess.State)
	return writeJSON(stdout, sess)
}

func cmdSessionGet(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("session get", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "override state directory")
	sessionID := fs.String("session", "", "session id (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" {
		return fmt.Errorf("session get: --session is required")
	}
	c, _, err := dialBroker(*stateDir)
	if err != nil {
		return err
	}
	sess, err := c.SessionGet(*sessionID)
	if err != nil {
		return err
	}
	return writeJSON(stdout, sess)
}

func cmdSessionEnd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("session end", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "override state directory")
	sessionID := fs.String("session", "", "session id (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" {
		return fmt.Errorf("session end: --session is required")
	}
	c, _, err := dialBroker(*stateDir)
	if err != nil {
		return err
	}
	sess, err := c.SessionEnd(*sessionID)
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "ended session %s (state=%s)\n", sess.ID, sess.State)
	return writeJSON(stdout, sess)
}

// --- plan ---

func cmdPlan(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("plan: expected subcommand (publish)")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "publish":
		return cmdPlanPublish(rest, stdin, stdout, stderr)
	default:
		return fmt.Errorf("plan: unknown subcommand %q", sub)
	}
}

func cmdPlanPublish(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("plan publish", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "override state directory")
	file := fs.String("file", "", "plan JSON file (reads stdin if omitted)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var data []byte
	var err error
	if *file != "" {
		data, err = readBoundedFile(*file)
	} else {
		data, err = io.ReadAll(io.LimitReader(stdin, protocol.MaxPlanBytes+1))
	}
	if err != nil {
		return err
	}
	// Validate locally first so a malformed plan fails with a clear client-side
	// diagnostic before touching the broker.
	if _, err := protocol.ParsePlan(data); err != nil {
		return fmt.Errorf("plan is invalid: %w", err)
	}

	c, _, err := dialBroker(*stateDir)
	if err != nil {
		return err
	}
	sess, err := c.PlanPublish(data)
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "published revision %d for session %s (state=%s)\n",
		sess.CurrentRevision, sess.ID, sess.State)
	return writeJSON(stdout, sess)
}

// --- decision ---

func cmdDecision(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("decision: expected subcommand (poll)")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "poll":
		return cmdDecisionPoll(rest, stdout, stderr)
	default:
		return fmt.Errorf("decision: unknown subcommand %q", sub)
	}
}

func cmdDecisionPoll(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("decision poll", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "override state directory")
	sessionID := fs.String("session", "", "session id (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" {
		return fmt.Errorf("decision poll: --session is required")
	}
	c, _, err := dialBroker(*stateDir)
	if err != nil {
		return err
	}
	res, err := c.DecisionPoll(*sessionID)
	if err != nil {
		return err
	}
	if res.Decided {
		fmt.Fprintf(stderr, "decision: %s\n", res.Decision.Kind)
	} else {
		fmt.Fprintln(stderr, "decision: pending")
	}
	return writeJSON(stdout, res)
}

// --- approval ---

func cmdApproval(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("approval: expected subcommand (claim)")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "claim":
		return cmdApprovalClaim(rest, stdout, stderr)
	default:
		return fmt.Errorf("approval: unknown subcommand %q", sub)
	}
}

func cmdApprovalClaim(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("approval claim", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "override state directory")
	sessionID := fs.String("session", "", "session id (required)")
	digest := fs.String("digest", "", "approval digest (sha256:...) (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" || *digest == "" {
		return fmt.Errorf("approval claim: --session and --digest are required")
	}
	c, _, err := dialBroker(*stateDir)
	if err != nil {
		return err
	}
	raw, err := c.ApprovalClaim(*sessionID, *digest)
	if err != nil {
		return err
	}
	fmt.Fprintln(stderr, "claim: granted")
	// raw is the ApprovalClaimResult JSON; re-emit it verbatim as the machine
	// output.
	var pretty json.RawMessage = raw
	return writeJSON(stdout, pretty)
}

// --- open ---

func cmdOpen(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "override state directory")
	sessionID := fs.String("session", "", "session id (required)")
	noBrowser := fs.Bool("no-browser", false, "print the URL but do not launch a browser")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" {
		return fmt.Errorf("open: --session is required")
	}
	c, _, err := dialBroker(*stateDir)
	if err != nil {
		return err
	}
	url, err := c.SessionOpen(*sessionID)
	if err != nil {
		return err
	}
	// Always print the URL so headless/test flows can use it.
	fmt.Fprintln(stdout, url)
	if *noBrowser {
		return nil
	}
	if err := openBrowser(url); err != nil {
		fmt.Fprintf(stderr, "could not launch browser (%v); open the URL above manually\n", err)
	}
	return nil
}

// openBrowser launches the OS browser for url. On macOS this uses `open`; on
// other platforms it falls back to xdg-open where present. Failure is
// non-fatal: the URL is already printed to stdout.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	default:
		return fmt.Errorf("no browser launcher for %s", runtime.GOOS)
	}
}
