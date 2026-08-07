package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run([]string{"version"}, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(out.String(), "control-room") || !strings.Contains(out.String(), "protocol v1") {
		t.Fatalf("unexpected version output: %q", out.String())
	}
}

func TestRunValidatePlanAcceptsValid(t *testing.T) {
	plan := `{"protocol_version":1,"session_id":"s-1","revision":2,"goal":"g","summary":"","workspace":{"id":"w","display_name":"d"},"blocks":[],"actions":[]}`
	var out, errOut bytes.Buffer
	if err := run([]string{"validate-plan"}, strings.NewReader(plan), &out, &errOut); err != nil {
		t.Fatalf("validate-plan: %v", err)
	}
	if !strings.HasPrefix(out.String(), "ok:") {
		t.Fatalf("expected ok output, got %q", out.String())
	}
}

func TestRunValidatePlanRejectsInvalid(t *testing.T) {
	plan := `{"protocol_version":99,"session_id":"s-1","revision":2,"goal":"g","summary":"","workspace":{"id":"w","display_name":"d"},"blocks":[],"actions":[]}`
	var out, errOut bytes.Buffer
	if err := run([]string{"validate-plan"}, strings.NewReader(plan), &out, &errOut); err == nil {
		t.Fatal("expected rejection of invalid plan")
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run([]string{"frobnicate"}, strings.NewReader(""), &out, &errOut); err == nil {
		t.Fatal("expected error on unknown command")
	}
}

func TestRunHelpDefault(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run(nil, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatalf("default help: %v", err)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Fatalf("expected usage text, got %q", out.String())
	}
}
