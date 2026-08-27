package config

import (
	"path/filepath"
	"testing"

	"github.com/smidley/gantry/internal/store"
	"github.com/stretchr/testify/require"
)

func testCfg(t *testing.T, env map[string]string) (*Config, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	return New(st, func(k string) string { return env[k] }), st
}

func TestPrecedenceEnvOverSettingOverDefault(t *testing.T) {
	c, st := testCfg(t, map[string]string{"GANTRY_PORT": "7777"})
	require.NoError(t, st.SettingSet("port", "9000"))
	require.Equal(t, 7777, c.Int("port", 8380)) // env wins

	c2, st2 := testCfg(t, nil)
	require.NoError(t, st2.SettingSet("port", "9000"))
	require.Equal(t, 9000, c2.Int("port", 8380)) // setting beats default

	c3, _ := testCfg(t, nil)
	require.Equal(t, 8380, c3.Int("port", 8380)) // default
}

func TestBoolAndKeyMapping(t *testing.T) {
	c, _ := testCfg(t, map[string]string{"GANTRY_FAKE_DATA": "true"})
	require.True(t, c.Bool("fake_data", false))

	c2, _ := testCfg(t, map[string]string{"GANTRY_RETENTION_R1_HOURS": "24"})
	require.Equal(t, 24, c2.Int("retention.r1_hours", 48)) // dots → underscores
}

func TestEnvOverridden(t *testing.T) {
	c, _ := testCfg(t, map[string]string{"GANTRY_RETENTION_R1_HOURS": "24"})
	require.True(t, c.EnvOverridden("retention.r1_hours"))
	require.False(t, c.EnvOverridden("retention.r2_days"), "no env var set for this key")

	c2, _ := testCfg(t, map[string]string{"GANTRY_RETENTION_R1_HOURS": ""})
	require.False(t, c2.EnvOverridden("retention.r1_hours"), "an empty env var is the same as unset")
}
