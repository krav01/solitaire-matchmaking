package migrations

import (
	"strings"
	"testing"
)

func TestLoadReturnsOrderedImmutableCatalog(t *testing.T) {
	t.Parallel()
	catalog, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(catalog) != 6 {
		t.Fatalf("Load() count = %d, want 6", len(catalog))
	}
	wantNames := []string{"initial_schema", "ticket_lifecycle", "matchmaking_worker", "result_finalization", "rating_worker", "outbox_delivery"}
	for index, migration := range catalog {
		if migration.Version != int64(index+1) || migration.Name != wantNames[index] {
			t.Fatalf("Load()[%d] = %+v", index, migration)
		}
		if len(migration.Checksum) != 64 || strings.TrimSpace(migration.SQL) == "" {
			t.Fatalf("Load()[%d] has invalid contents or checksum", index)
		}
	}

	second, err := Load()
	if err != nil {
		t.Fatalf("second Load() error = %v", err)
	}
	for index := range catalog {
		if second[index] != catalog[index] {
			t.Fatal("embedded migration catalog changed between reads")
		}
	}
}

func TestParseFilenameRejectsInvalidNames(t *testing.T) {
	t.Parallel()
	for _, filename := range []string{
		"1_initial.up.sql",
		"000000_initial.up.sql",
		"000001_.up.sql",
		"abcdef_initial.up.sql",
		"000001_initial.sql",
	} {
		if _, _, err := parseFilename(filename); err == nil {
			t.Fatalf("parseFilename(%q) error = nil", filename)
		}
	}
}
