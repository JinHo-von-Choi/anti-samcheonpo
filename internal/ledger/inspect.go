package ledger

import (
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
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

func openReadOnly(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	db, err := sql.Open("sqlite", u.String()+"?mode=ro&_pragma=busy_timeout(200)&_pragma=query_only(1)")
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
