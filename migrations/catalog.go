// Package migrations exposes the immutable, embedded PostgreSQL migration catalog.
package migrations

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
)

//go:embed *.up.sql
var files embed.FS

// Migration is one forward-only schema change.
type Migration struct {
	Version  int64
	Name     string
	SQL      string
	Checksum string
}

// Load returns all embedded migrations in version order.
func Load() ([]Migration, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	loaded := make([]Migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		version, name, err := parseFilename(entry.Name())
		if err != nil {
			return nil, err
		}
		contents, err := files.ReadFile(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		digest := sha256.Sum256(contents)
		loaded = append(loaded, Migration{
			Version:  version,
			Name:     name,
			SQL:      string(contents),
			Checksum: hex.EncodeToString(digest[:]),
		})
	}

	slices.SortFunc(loaded, func(left, right Migration) int {
		return int(left.Version - right.Version)
	})
	for index, migration := range loaded {
		if index > 0 && loaded[index-1].Version == migration.Version {
			return nil, fmt.Errorf("duplicate migration version %d", migration.Version)
		}
	}
	if len(loaded) == 0 {
		return nil, fmt.Errorf("embedded migration catalog is empty")
	}
	return loaded, nil
}

func parseFilename(filename string) (int64, string, error) {
	if !strings.HasSuffix(filename, ".up.sql") {
		return 0, "", fmt.Errorf("migration filename %q must match NNNNNN_name.up.sql", filename)
	}
	base := strings.TrimSuffix(filename, ".up.sql")
	versionText, name, found := strings.Cut(base, "_")
	if !found || len(versionText) != 6 || name == "" {
		return 0, "", fmt.Errorf("migration filename %q must match NNNNNN_name.up.sql", filename)
	}
	version, err := strconv.ParseInt(versionText, 10, 64)
	if err != nil || version <= 0 {
		return 0, "", fmt.Errorf("migration filename %q has an invalid version", filename)
	}
	return version, name, nil
}
