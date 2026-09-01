package store

import (
	"errors"
	"io/fs"

	sqlite "modernc.org/sqlite"
)

// sqliteCantOpen is SQLite's SQLITE_CANTOPEN primary result code (14):
// the library couldn't open the database file. In a container the usual
// cause is that the mounted config directory isn't writable by the
// process uid, so the file (and its -wal/-shm siblings) can't be created.
const sqliteCantOpen = 14

// IsCantOpen reports whether err is a "can't open the database file" /
// permission failure from Open -- the shape Gantry hits when /config isn't
// writable by the container's uid (issue #37). It recognizes the modernc
// driver's typed *sqlite.Error by primary result code (masking off the
// extended-code high bits so SQLITE_CANTOPEN_* variants still match) and,
// defensively, a wrapped OS permission error in case a future driver
// surfaces one directly instead of a SQLite code.
func IsCantOpen(err error) bool {
	if err == nil {
		return false
	}
	var se *sqlite.Error
	if errors.As(err, &se) && se.Code()&0xff == sqliteCantOpen {
		return true
	}
	return errors.Is(err, fs.ErrPermission)
}
