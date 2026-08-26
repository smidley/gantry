package collect

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSlugSegmentLowercases(t *testing.T) {
	require.Equal(t, "abc", SlugSegment("ABC"))
}

func TestSlugSegmentReplacesInvalidCharsAndLowercases(t *testing.T) {
	require.Equal(t, "my_movies_4k", SlugSegment("My Movies.4K"))
}

func TestSlugSegmentCollapsesRunsOfInvalidChars(t *testing.T) {
	require.Equal(t, "a_b", SlugSegment("a!!!b"))
	require.Equal(t, "a_b", SlugSegment("a   b"))
}

func TestSlugSegmentTrimsLeadingAndTrailingUnderscores(t *testing.T) {
	require.Equal(t, "hidden", SlugSegment(".hidden."))
}

func TestSlugSegmentEmptyResultBecomesUnknown(t *testing.T) {
	require.Equal(t, "unknown", SlugSegment(""))
	require.Equal(t, "unknown", SlugSegment("..."))
	require.Equal(t, "unknown", SlugSegment("!!!"))
}

func TestSlugSegmentPreservesAllowedCharsUnchanged(t *testing.T) {
	require.Equal(t, "video-enhance", SlugSegment("video-enhance"))
	require.Equal(t, "already_clean-name", SlugSegment("already_clean-name"))
}

// Regression pins: every one of these is a metric-name segment that real
// fixtures already produce today, byte-identical before this helper
// existed. History continuity for existing series depends on
// SlugSegment being a no-op on all of them.
func TestSlugSegmentRegressionPinsForExistingCleanMetricNames(t *testing.T) {
	require.Equal(t, "coretemp_package_id_0", SlugSegment("coretemp Package id 0"))
	require.Equal(t, "nct6779_fan1", SlugSegment("nct6779 fan1"))
	require.Equal(t, "appdata", SlugSegment("appdata"))
	require.Equal(t, "media", SlugSegment("media"))
	require.Equal(t, "video-enhance", SlugSegment("video-enhance"))
	require.Equal(t, "sda", SlugSegment("sda"))
	require.Equal(t, "sdb", SlugSegment("sdb"))
}
