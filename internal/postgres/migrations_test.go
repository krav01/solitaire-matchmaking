package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/krav01/solitaire-matchmaking/migrations"
)

func TestApplyMigrationsAppliesPendingCatalogAtomically(t *testing.T) {
	t.Parallel()
	tx := &fakeMigrationTx{rows: []fakeMigrationRow{{err: pgx.ErrNoRows}}}
	catalog := []migrations.Migration{{Version: 1, Name: "initial", SQL: "CREATE TABLE example (id bigint)", Checksum: strings.Repeat("a", 64)}}

	applied, err := applyMigrations(context.Background(), fakeMigrationStarter{tx: tx}, catalog)
	if err != nil {
		t.Fatalf("applyMigrations() error = %v", err)
	}
	if applied != 1 || !tx.committed || tx.rollbackCalls != 1 {
		t.Fatalf("applyMigrations() = %d, committed = %v, rollbacks = %d", applied, tx.committed, tx.rollbackCalls)
	}
	if len(tx.execs) != 4 {
		t.Fatalf("Exec() calls = %d, want lock, ledger, migration and insert", len(tx.execs))
	}
	if !strings.Contains(tx.execs[3].sql, "INSERT INTO schema_migrations") {
		t.Fatalf("last Exec() = %q, want migration ledger insert", tx.execs[3].sql)
	}
	if got := tx.execs[3].arguments; len(got) != 3 || got[0] != int64(1) || got[1] != "initial" {
		t.Fatalf("migration insert arguments = %#v", got)
	}
}

func TestApplyMigrationsSkipsMatchingAppliedMigration(t *testing.T) {
	t.Parallel()
	checksum := strings.Repeat("b", 64)
	tx := &fakeMigrationTx{rows: []fakeMigrationRow{{values: []any{"initial", checksum}}}}
	catalog := []migrations.Migration{{Version: 1, Name: "initial", SQL: "invalid SQL must not run", Checksum: checksum}}

	applied, err := applyMigrations(context.Background(), fakeMigrationStarter{tx: tx}, catalog)
	if err != nil {
		t.Fatalf("applyMigrations() error = %v", err)
	}
	if applied != 0 || len(tx.execs) != 2 || !tx.committed {
		t.Fatalf("applied = %d, Exec() calls = %d, committed = %v", applied, len(tx.execs), tx.committed)
	}
}

func TestApplyMigrationsRejectsChangedAppliedMigration(t *testing.T) {
	t.Parallel()
	tx := &fakeMigrationTx{rows: []fakeMigrationRow{{values: []any{"initial", strings.Repeat("c", 64)}}}}
	catalog := []migrations.Migration{{Version: 1, Name: "initial", SQL: "SELECT 1", Checksum: strings.Repeat("d", 64)}}

	applied, err := applyMigrations(context.Background(), fakeMigrationStarter{tx: tx}, catalog)
	if err == nil || !strings.Contains(err.Error(), "differs from the applied checksum") {
		t.Fatalf("applyMigrations() error = %v", err)
	}
	if applied != 0 || tx.committed || tx.rollbackCalls != 1 {
		t.Fatalf("applied = %d, committed = %v, rollbacks = %d", applied, tx.committed, tx.rollbackCalls)
	}
}

func TestApplyMigrationsRollsBackFailedMigration(t *testing.T) {
	t.Parallel()
	tx := &fakeMigrationTx{
		rows:       []fakeMigrationRow{{err: pgx.ErrNoRows}},
		failSQL:    "BROKEN",
		failureErr: errors.New("database rejected migration"),
	}
	catalog := []migrations.Migration{{Version: 2, Name: "broken", SQL: "BROKEN", Checksum: strings.Repeat("e", 64)}}

	_, err := applyMigrations(context.Background(), fakeMigrationStarter{tx: tx}, catalog)
	if err == nil || !strings.Contains(err.Error(), "apply migration 2 (broken)") {
		t.Fatalf("applyMigrations() error = %v", err)
	}
	if tx.committed || tx.rollbackCalls != 1 {
		t.Fatalf("committed = %v, rollbacks = %d", tx.committed, tx.rollbackCalls)
	}
}

func TestApplyMigrationsRequiresPool(t *testing.T) {
	t.Parallel()
	if _, err := ApplyMigrations(context.Background(), nil); err == nil {
		t.Fatal("ApplyMigrations() error = nil")
	}
}

type fakeMigrationStarter struct {
	tx  migrationTx
	err error
}

func (starter fakeMigrationStarter) Begin(context.Context) (migrationTx, error) {
	return starter.tx, starter.err
}

type execCall struct {
	sql       string
	arguments []any
}

type fakeMigrationTx struct {
	rows          []fakeMigrationRow
	execs         []execCall
	failSQL       string
	failureErr    error
	committed     bool
	rollbackCalls int
}

func (tx *fakeMigrationTx) Exec(_ context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	tx.execs = append(tx.execs, execCall{sql: sql, arguments: arguments})
	if sql == tx.failSQL {
		return pgconn.CommandTag{}, tx.failureErr
	}
	return pgconn.NewCommandTag("OK"), nil
}

func (tx *fakeMigrationTx) QueryRow(context.Context, string, ...any) pgx.Row {
	if len(tx.rows) == 0 {
		return fakeMigrationRow{err: fmt.Errorf("unexpected QueryRow call")}
	}
	row := tx.rows[0]
	tx.rows = tx.rows[1:]
	return row
}

func (tx *fakeMigrationTx) Commit(context.Context) error {
	tx.committed = true
	return nil
}

func (tx *fakeMigrationTx) Rollback(context.Context) error {
	tx.rollbackCalls++
	return nil
}

type fakeMigrationRow struct {
	values []any
	err    error
}

func (row fakeMigrationRow) Scan(dest ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(dest) != len(row.values) {
		return fmt.Errorf("Scan destinations = %d, values = %d", len(dest), len(row.values))
	}
	for index, value := range row.values {
		target, ok := dest[index].(*string)
		if !ok {
			return fmt.Errorf("unsupported Scan destination %T", dest[index])
		}
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("unsupported Scan value %T", value)
		}
		*target = text
	}
	return nil
}
