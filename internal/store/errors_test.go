package store

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIsCantOpenRecognizesRealCantOpen drives the real driver: opening a
// database whose parent directory doesn't exist is SQLite's SQLITE_CANTOPEN
// (code 14) -- the exact shape a container hits when /config isn't writable
// by its uid (issue #37). IsCantOpen must recognize it so the caller can
// attach the DAC_OVERRIDE hint.
func TestIsCantOpenRecognizesRealCantOpen(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "no-such-subdir", "gantry.db"), nil)
	require.Error(t, err)
	require.True(t, IsCantOpen(err), "a real SQLITE_CANTOPEN must classify as can't-open, got %v", err)
}

// TestIsCantOpenTrueForWrappedPermission covers the defensive OS-permission
// branch: should a driver ever surface a plain permission error instead of a
// typed SQLite code, it must still read as can't-open.
func TestIsCantOpenTrueForWrappedPermission(t *testing.T) {
	require.True(t, IsCantOpen(fmt.Errorf("open db: %w", fs.ErrPermission)))
}

// TestIsCantOpenFalseForUnrelated pins the negative: nil and ordinary errors
// (a genuinely different failure like a corrupt migration) must NOT get the
// permission hint bolted on.
func TestIsCantOpenFalseForUnrelated(t *testing.T) {
	require.False(t, IsCantOpen(nil))
	require.False(t, IsCantOpen(errors.New("some other failure")))
}

// TestOpenStillWorksOnWritablePath is the "a normal open still works" half:
// the new classifier changes nothing about the happy path.
func TestOpenStillWorksOnWritablePath(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "gantry.db"), nil)
	require.NoError(t, err)
	require.False(t, IsCantOpen(err))
	require.NoError(t, st.Close())
}
