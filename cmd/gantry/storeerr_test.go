package main

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStoreOpenErrorAddsHintForCantOpen: a permission/can't-open failure
// (issue #37) must be wrapped with an actionable hint that names the
// directory to fix and the DAC_OVERRIDE cap the CA template already sets --
// without swallowing the original error.
func TestStoreOpenErrorAddsHintForCantOpen(t *testing.T) {
	orig := fmt.Errorf("unable to open database file: %w", fs.ErrPermission)
	got := storeOpenError("/config/gantry.db", orig)

	require.Error(t, got)
	msg := got.Error()
	require.Contains(t, msg, "/config/gantry.db", "the wrapped error names the db path")
	require.Contains(t, msg, "/config", "the hint names the directory that needs write access")
	require.Contains(t, msg, "DAC_OVERRIDE", "the hint points at the cap that fixes it")
	require.ErrorIs(t, got, fs.ErrPermission, "the original error must stay unwrapped-through")
}

// TestStoreOpenErrorPlainWrapForOther: an unrelated failure keeps the plain
// wrap -- no misleading permission advice bolted onto a different problem.
func TestStoreOpenErrorPlainWrapForOther(t *testing.T) {
	orig := errors.New("migration 7 failed")
	got := storeOpenError("/config/gantry.db", orig)

	require.Error(t, got)
	require.Contains(t, got.Error(), "open store")
	require.NotContains(t, got.Error(), "DAC_OVERRIDE")
	require.ErrorIs(t, got, orig)
}

// TestStoreOpenErrorHintTracksDBPathDir: the directory in the hint follows
// GANTRY_DB_PATH rather than being hardcoded to /config.
func TestStoreOpenErrorHintTracksDBPathDir(t *testing.T) {
	orig := fmt.Errorf("cantopen: %w", fs.ErrPermission)
	got := storeOpenError("/data/gantry/gantry.db", orig)
	require.Contains(t, got.Error(), "/data/gantry", "hint must reflect the actual db directory")
	require.NotContains(t, got.Error(), "write access to /config", "must not hardcode /config")
}
