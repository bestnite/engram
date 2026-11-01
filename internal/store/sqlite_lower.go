package store

import (
	"database/sql/driver"
	"strings"

	"github.com/glebarez/go-sqlite"
)

// sqliteLowerFunc is the SQL name of the Unicode-aware lower-case function used on SQLite.
const sqliteLowerFunc = "engram_lower"

// init registers the function on every SQLite connection this process opens.
//
// SQLite's built-in LOWER folds ASCII only, so a tag stored as "Été" is not matched by its own
// lower-case spelling, while PostgreSQL's LOWER folds the whole of Unicode. The tag filter folds
// both sides of its comparison, so SQLite needs a folding rule that agrees with the Go code
// building the pattern (strings.ToLower) instead of the ASCII-only built-in.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction(sqliteLowerFunc, 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		s, ok := args[0].(string)
		if !ok {
			// NULL stays NULL, so a NULL column behaves as it does under LOWER.
			return nil, nil
		}
		return strings.ToLower(s), nil
	})
}
