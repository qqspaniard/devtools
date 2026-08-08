// Command control-room is the single binary described in the Control Room RFC.
//
// Phase 0 is a foundation slice: the protocol types, resource limits, session
// state machine, and approval digest/claim policy exist and are tested, but the
// broker, Unix socket, embedded SQLite, and browser GUI do not yet exist. This
// entry point is therefore intentionally minimal — enough to compile, print
// version/build information, and demonstrate that a plan can be parsed and an
// approval digest computed without any I/O beyond stdin/stdout.
//
// The subcommand surface from the RFC (open, session create, plan publish,
// decision poll, ...) is deferred to Phase 1, when the broker and its transport
// land.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/interactionlabs/devtools/control-room/internal/protocol"
)

// version is the Phase 0 marker. It is not a release version.
const version = "0.0.0-phase0"

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
	switch cmd {
	case "version":
		fmt.Fprintf(stdout, "control-room %s (protocol v%d)\n", version, protocol.ProtocolVersion)
		return nil
	case "validate-plan":
		// Read a plan revision as JSON from stdin and report whether it is a
		// valid Phase 0 plan. This exercises the protocol package end-to-end
		// without any broker machinery, and is useful for adapter authors.
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
	case "help", "-h", "--help":
		printHelp(stdout)
		return nil
	default:
		printHelp(stderr)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func printHelp(w io.Writer) {
	fmt.Fprintf(w, `control-room %s — Phase 0 foundation

This build contains the protocol, policy, and approval-digest foundation only.
The broker, agent socket, SQLite store, and browser GUI are not yet implemented.

Usage:
  control-room version         Print version and protocol number.
  control-room validate-plan   Read a plan revision (JSON) from stdin and
                               validate it against the Phase 0 protocol.
  control-room help            Show this help.
`, version)
}
