package main

import (
	"bytes"
	"testing"
)

func TestRunProducesReproducibleJSON(t *testing.T) {
	t.Parallel()
	arguments := []string{"-seed", "17", "-tickets", "5", "-arrival-rate", "2", "-start", "2026-09-04T09:00:00Z"}
	var first bytes.Buffer
	if err := run(arguments, &first); err != nil {
		t.Fatalf("first run() error = %v", err)
	}
	var second bytes.Buffer
	if err := run(arguments, &second); err != nil {
		t.Fatalf("second run() error = %v", err)
	}
	if first.String() != second.String() {
		t.Fatal("run() output changed for the same arguments")
	}
}

func TestRunRejectsInvalidArguments(t *testing.T) {
	t.Parallel()
	if err := run([]string{"-tickets", "0"}, &bytes.Buffer{}); err == nil {
		t.Fatal("run() error = nil for invalid ticket count")
	}
	if err := run([]string{"unexpected"}, &bytes.Buffer{}); err == nil {
		t.Fatal("run() error = nil for positional argument")
	}
}
