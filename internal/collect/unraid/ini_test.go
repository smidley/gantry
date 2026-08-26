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

// TestParseINIHandlesEmptyQuotedValue is driven by a real shape: var.ini
// captured from a live Unraid 7.3.2 box carries dozens of keys like
// DOMAIN="" — a pair of adjacent quotes, not a missing value.
func TestParseINIHandlesEmptyQuotedValue(t *testing.T) {
	kv, err := ParseINI(strings.NewReader(`DOMAIN=""
mdState="STARTED"
`))
	require.NoError(t, err)
	value, ok := kv[""]["DOMAIN"]
	require.True(t, ok, "an empty-quoted value must still produce a present key")
	require.Equal(t, "", value)
	require.Equal(t, "STARTED", kv[""]["mdState"])
}

// TestParseFloatOKHandlesDecimalStringsLikeRealFloorField is driven by a
// real shape: shares.ini captured from a live Unraid 7.3.2 box has at
// least one numeric-looking field (a share's "floor") whose value is
// "17950564.8" — a decimal, not an integer. No field this package reads
// was observed with a fractional value, but parseFloatOK must not choke
// if one ever is, since it already uses ParseFloat rather than ParseInt.
func TestParseFloatOKHandlesDecimalStringsLikeRealFloorField(t *testing.T) {
	f, ok := parseFloatOK("17950564.8")
	require.True(t, ok)
	require.InDelta(t, 17950564.8, f, 1e-9)
}

// errReader always fails, proving ParseINI surfaces a genuine reader
// error rather than swallowing it the way it swallows malformed content.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestParseINIReturnsReaderError(t *testing.T) {
	_, err := ParseINI(errReader{})
	require.Error(t, err)
}
