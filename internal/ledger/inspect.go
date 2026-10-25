package ledger

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Inspect opens an existing database read-only; it never creates or migrates it.
func Inspect(path string) (int, error) {
	db, err := openReadOnly(path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	return inspectSchema(db)
}

// InspectWithBudget behaves like Inspect but skips the size-proportional
// quick_check on ledgers bigger than quickCheckLimit bytes. Skipping is
// reported so the caller can say what was and was not verified instead of
// letting a multi-minute scan read as a hang. The schema read still runs.
func InspectWithBudget(path string) (version int, skippedQuickCheck bool, err error) {
	db, err := openReadOnly(path)
	if err != nil {
		return 0, false, err
	}
	defer db.Close()
	if FileSize(path) > quickCheckLimit {
		version, err = inspectSchemaVersion(db)
		return version, true, err
	}
	version, err = inspectSchema(db)
	return version, false, err
}

// quickCheckLimit balances two claims a doctor must not make silently:
// PRAGMA quick_check grows linearly with the file (about a minute for a
// 600MB ledger), and every check above this line trades a longer scan for
// a narrower one where honesty about skipping matters. 256MB caps the
// scan at a few seconds while smaller ledgers keep the full check.
const quickCheckLimit = 256 * 1024 * 1024

func openReadOnly(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", fileURI(abs)+"?mode=ro&_pragma=busy_timeout(200)&_pragma=query_only(1)")
	return db, err
}

func inspectSchema(db *sql.DB) (int, error) {
	var status string
	if err := db.QueryRow(`PRAGMA quick_check(1)`).Scan(&status); err != nil {
		return 0, err
	}
	if status != "ok" {
		return 0, fmt.Errorf("ledger integrity check failed")
	}
	return inspectSchemaVersion(db)
}

// inspectSchemaVersion reads the stored schema version and compares it
// with the migrations compiled into this binary. Verifying the migrations
// keeps future and stale ledgers from entering the daemon.
func inspectSchemaVersion(db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&version); err != nil {
		return 0, err
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return 0, err
	}
	latest := 0
	for _, name := range names {
		n, err := strconv.Atoi(strings.SplitN(filepath.Base(name), "_", 2)[0])
		if err != nil {
			return 0, err
		}
		latest = max(latest, n)
	}
	if version > latest {
		return version, fmt.Errorf("unsupported future ledger version")
	}
	if version < latest {
		return version, fmt.Errorf("ledger migration required")
	}
	return version, nil
}
