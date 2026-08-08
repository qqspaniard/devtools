// Command control-room is the single binary described in the Control Room RFC.
//
// This build implements the usable vertical slice: a long-lived local broker
// (control Unix socket + loopback browser review surface + durable SQLite
// store) and the CLI operations that drive the plan → approve → claim loop.
//
// Commands:
//
//	control-room version
//	control-room validate-plan            (stdin JSON)
//	control-room serve                    (run the broker; blocks)
//	control-room session create           --workspace-id ... [--workspace-name ...]
//	control-room session get              --session ...
//	control-room session end              --session ...
//	control-room plan publish             [--file plan.json | stdin]
//	control-room decision poll            --session ...
//	control-room approval claim           --session ... --digest ...
//	control-room open                     --session ... [--no-browser]
//
// Machine-readable JSON is written to stdout for operations an adapter would
// consume; human diagnostics go to stderr. The state directory is configurable
// via --state-dir or CONTROL_ROOM_STATE_DIR (default per RFC).
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/interactionlabs/devtools/control-room/internal/protocol"
)

// version is the usable-loop marker. It is not a release version.
const version = "0.0.0-usable-loop"

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "control-room:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := "help"
	if len(args) > 0 {
		cmd = args[0]
	}
	var rest []string
	if len(args) > 1 {
		rest = args[1:]
	}

	switch cmd {
	case "version":
		fmt.Fprintf(stdout, "control-room %s (protocol v%d)\n", version, protocol.ProtocolVersion)
		return nil
	case "validate-plan":
		return cmdValidatePlan(stdin, stdout)
	case "serve":
		return cmdServe(rest, stdout, stderr)
	case "session":
		return cmdSession(rest, stdin, stdout, stderr)
	case "plan":
		return cmdPlan(rest, stdin, stdout, stderr)
	case "decision":
		return cmdDecision(rest, stdout, stderr)
	case "approval":
		return cmdApproval(rest, stdout, stderr)
	case "open":
		return cmdOpen(rest, stdout, stderr)
	case "help", "-h", "--help":
		printHelp(stdout)
		return nil
	default:
		printHelp(stderr)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func cmdValidatePlan(stdin io.Reader, stdout io.Writer) error {
	data, err := io.ReadAll(io.LimitReader(stdin, protocol.MaxPlanBytes+1))
	if err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}
	plan, err := protocol.ParsePlan(data)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "ok: session=%s revision=%d actions=%d blocks=%d\n",
		plan.SessionID, plan.Revision, len(plan.Actions), len(plan.Blocks))
	return nil
}

func printHelp(w io.Writer) {
	fmt.Fprintf(w, `control-room %s

Usage:
  control-room version
  control-room validate-plan                 Validate a plan (JSON) from stdin.
  control-room serve [--state-dir DIR]       Run the local broker (blocks until
                                             interrupted). Prints the socket and
                                             loopback review base URL.

  control-room session create --workspace-id ID [--workspace-name NAME]
  control-room session get    --session ID
  control-room session end    --session ID
  control-room plan publish   [--file plan.json]     (reads stdin if no --file)
  control-room decision poll  --session ID
  control-room approval claim --session ID --digest sha256:...
  control-room open           --session ID [--no-browser]

Global:
  --state-dir DIR    Override the state directory (default: per-RFC
                     ~/.local/state/control-room; env CONTROL_ROOM_STATE_DIR).

Machine-readable JSON is written to stdout for session/plan/decision/approval
operations; human diagnostics go to stderr.
`, version)
}
