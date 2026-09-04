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
	if len(catalog) != 1 {
		t.Fatalf("Load() count = %d, want 1", len(catalog))
	}
	migration := catalog[0]
	if migration.Version != 1 || migration.Name != "initial_schema" {
		t.Fatalf("Load()[0] = %+v", migration)
	}
	if len(migration.Checksum) != 64 || strings.TrimSpace(migration.SQL) == "" {
		t.Fatalf("Load()[0] has invalid contents or checksum")
	}

	second, err := Load()
	if err != nil {
		t.Fatalf("second Load() error = %v", err)
	}
	if second[0] != migration {
		t.Fatal("embedded migration catalog changed between reads")
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
