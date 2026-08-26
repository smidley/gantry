package unraid

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseINIQuotedValuesGoToHeaderlessSection(t *testing.T) {
	kv, err := ParseINI(strings.NewReader(`mdState="STARTED"
version="7.3.2"
`))
	require.NoError(t, err)
	require.Equal(t, "STARTED", kv[""]["mdState"])
	require.Equal(t, "7.3.2", kv[""]["version"])
}

func TestParseINIQuotedSectionHeaderStripsQuotes(t *testing.T) {
	kv, err := ParseINI(strings.NewReader(`["disk1"]
name="disk1"
status="DISK_OK"
`))
	require.NoError(t, err)
	require.Equal(t, "disk1", kv["disk1"]["name"])
	require.Equal(t, "DISK_OK", kv["disk1"]["status"])
	_, hasQuotedKey := kv[`"disk1"`]
	require.False(t, hasQuotedKey, "the section key must be the unquoted name, not the literal bracket contents")
}

func TestParseINIBareSectionHeaderAlsoAccepted(t *testing.T) {
	kv, err := ParseINI(strings.NewReader(`[disk1]
name="disk1"
`))
	require.NoError(t, err)
	require.Equal(t, "disk1", kv["disk1"]["name"])
}

func TestParseINISkipsBlankAndMalformedLinesSilently(t *testing.T) {
	kv, err := ParseINI(strings.NewReader(`mdState="STARTED"

this is not a valid line
noquotes=bare
half="unterminated
=novalue
version="7.3.2"
`))
	require.NoError(t, err)
	require.Equal(t, "STARTED", kv[""]["mdState"])
	require.Equal(t, "7.3.2", kv[""]["version"])
	require.Len(t, kv[""], 2, "every blank/malformed line must be skipped, contributing no key")
}

func TestParseINISwitchesSectionAcrossMultipleHeadersAndKeepsHeaderlessSeparate(t *testing.T) {
	kv, err := ParseINI(strings.NewReader(`headerlessKey="top"
["disk1"]
name="disk1"
["disk2"]
name="disk2"
`))
	require.NoError(t, err)
	require.Equal(t, "top", kv[""]["headerlessKey"])
	require.Equal(t, "disk1", kv["disk1"]["name"])
	require.Equal(t, "disk2", kv["disk2"]["name"])
}

// errReader always fails, proving ParseINI surfaces a genuine reader
// error rather than swallowing it the way it swallows malformed content.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestParseINIReturnsReaderError(t *testing.T) {
	_, err := ParseINI(errReader{})
	require.Error(t, err)
}
