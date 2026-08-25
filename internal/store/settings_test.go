package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSettingsRoundTrip(t *testing.T) {
	s := newTestStore(t, nil)

	_, ok, err := s.SettingGet("port")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, s.SettingSet("port", "9000"))
	v, ok, err := s.SettingGet("port")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "9000", v)

	require.NoError(t, s.SettingSet("port", "9001")) // upsert
	v, _, _ = s.SettingGet("port")
	require.Equal(t, "9001", v)
}
