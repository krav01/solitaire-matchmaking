package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunValidatesInputsBeforeOpeningPostgreSQL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		arguments []string
		values    map[string]string
		wantError string
	}{
		{name: "candidate", wantError: "candidate-version"},
		{name: "database", arguments: []string{"-candidate-version", "candidate-v1"}, wantError: "DATABASE_URL"},
		{
			name: "policy", arguments: []string{"-candidate-version", "candidate-v1"},
			values: map[string]string{"DATABASE_URL": "postgres://unused"}, wantError: "RATING_SHADOW_COMPARISON_POLICY",
		},
		{
			name: "bins", arguments: []string{"-candidate-version", "candidate-v1"},
			values: map[string]string{
				"DATABASE_URL": "postgres://unused", "RATING_SHADOW_COMPARISON_POLICY": "{}",
				"RATING_SHADOW_BIN_COUNT": "many",
			}, wantError: "must be an integer",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			err := run(context.Background(), tt.arguments, func(key string) string { return tt.values[key] }, &output)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("run() error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}
