package main

import (
	"bytes"
	"encoding/json"
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
	var report struct {
		Overall struct {
			Tickets int `json:"tickets"`
		} `json:"overall"`
	}
	if err := json.Unmarshal(first.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Overall.Tickets != 5 {
		t.Fatalf("report tickets = %d, want 5", report.Overall.Tickets)
	}
}

func TestRunCanEmitWorkload(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := run([]string{"-output", "workload", "-tickets", "2"}, &output); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	var workload struct {
		Arrivals []json.RawMessage `json:"arrivals"`
	}
	if err := json.Unmarshal(output.Bytes(), &workload); err != nil {
		t.Fatalf("decode workload: %v", err)
	}
	if len(workload.Arrivals) != 2 {
		t.Fatalf("workload arrivals = %d, want 2", len(workload.Arrivals))
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
	if err := run([]string{"-output", "unknown"}, &bytes.Buffer{}); err == nil {
		t.Fatal("run() error = nil for invalid output type")
	}
}
