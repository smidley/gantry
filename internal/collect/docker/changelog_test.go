package docker

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestChangelogAndProjectURLs is a table test over the priority chain:
// a github.com org.opencontainers.image.source label wins outright; a
// ghcr.io image ref is the fallback when that label is absent or isn't
// a github.com URL; org.opencontainers.image.url always feeds
// projectURL independently of whichever (if any) of those two produced
// changelogURL -- see the two real-box cases below for why that's
// independent rather than a third rung of the same either-or chain.
func TestChangelogAndProjectURLs(t *testing.T) {
	cases := []struct {
		name             string
		labels           map[string]string
		image            string
		wantChangelogURL string
		wantProjectURL   string
	}{
		{
			name: "real box: jellyfin has both a github source label and a project url label",
			labels: map[string]string{
				ociSourceLabel: "https://github.com/jellyfin/jellyfin-packaging",
				ociURLLabel:    "https://jellyfin.org",
			},
			image:            "jellyfin/jellyfin:latest",
			wantChangelogURL: "https://github.com/jellyfin/jellyfin-packaging/releases",
			wantProjectURL:   "https://jellyfin.org",
		},
		{
			name:             "real box: optimisarr has no labels at all, falls back to its ghcr.io image ref",
			labels:           nil,
			image:            "ghcr.io/jellman86/optimisarr:latest",
			wantChangelogURL: "https://github.com/jellman86/optimisarr/releases",
			wantProjectURL:   "",
		},
		{
			name:             "source label trailing slash is stripped before appending /releases",
			labels:           map[string]string{ociSourceLabel: "https://github.com/foo/bar/"},
			image:            "foo/bar:latest",
			wantChangelogURL: "https://github.com/foo/bar/releases",
		},
		{
			name:             "source label .git suffix is stripped before appending /releases",
			labels:           map[string]string{ociSourceLabel: "https://github.com/foo/bar.git"},
			image:            "foo/bar:latest",
			wantChangelogURL: "https://github.com/foo/bar/releases",
		},
		{
			name:             "source label with both a .git suffix and a trailing slash",
			labels:           map[string]string{ociSourceLabel: "https://github.com/foo/bar.git/"},
			image:            "foo/bar:latest",
			wantChangelogURL: "https://github.com/foo/bar/releases",
		},
		{
			name:             "source label pointing somewhere other than github.com falls back to the ghcr.io image ref",
			labels:           map[string]string{ociSourceLabel: "https://gitlab.com/foo/bar"},
			image:            "ghcr.io/foo/bar:latest",
			wantChangelogURL: "https://github.com/foo/bar/releases",
		},
		{
			name:             "non-ghcr image with no usable source label gets no changelog",
			labels:           map[string]string{ociSourceLabel: "https://gitlab.com/foo/bar"},
			image:            "docker.io/foo/bar:latest",
			wantChangelogURL: "",
		},
		{
			name:             "no labels and no ghcr.io image gets nothing at all",
			labels:           nil,
			image:            "postgres:16",
			wantChangelogURL: "",
			wantProjectURL:   "",
		},
		{
			name:             "ghcr.io image with an extra path segment beyond owner/repo",
			labels:           nil,
			image:            "ghcr.io/owner/repo/extra:1.2.3",
			wantChangelogURL: "https://github.com/owner/repo/releases",
		},
		{
			name:             "ghcr.io image pinned by digest, no tag",
			labels:           nil,
			image:            "ghcr.io/owner/repo@sha256:deadbeef",
			wantChangelogURL: "https://github.com/owner/repo/releases",
		},
		{
			name:             "project url label present even though no changelog could be derived",
			labels:           map[string]string{ociURLLabel: "https://example.org"},
			image:            "docker.io/foo/bar:latest",
			wantChangelogURL: "",
			wantProjectURL:   "https://example.org",
		},
		{
			name:             "real box: an lscr.io image (LinuxServer.io's own mirror, not ghcr.io) gets no changelog -- only ghcr.io is special-cased",
			labels:           nil,
			image:            "lscr.io/linuxserver/sonarr:latest",
			wantChangelogURL: "",
		},
		{
			name:             "source label names an owner with no repo segment falls back to the ghcr.io image ref, same as no source label at all",
			labels:           map[string]string{ociSourceLabel: "https://github.com/someorg"},
			image:            "ghcr.io/someorg/somerepo:latest",
			wantChangelogURL: "https://github.com/someorg/somerepo/releases",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotChangelog, gotProject := changelogAndProjectURLs(c.labels, c.image)
			require.Equal(t, c.wantChangelogURL, gotChangelog)
			require.Equal(t, c.wantProjectURL, gotProject)
		})
	}
}

// TestGithubReleasesURL pins the github.com-source-label half of the
// derivation in isolation, including its non-github rejection.
func TestGithubReleasesURL(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   string
		wantOK bool
	}{
		{name: "plain repo url", source: "https://github.com/jellyfin/jellyfin-packaging", want: "https://github.com/jellyfin/jellyfin-packaging/releases", wantOK: true},
		{name: "trailing slash", source: "https://github.com/foo/bar/", want: "https://github.com/foo/bar/releases", wantOK: true},
		{name: "dot-git suffix", source: "https://github.com/foo/bar.git", want: "https://github.com/foo/bar/releases", wantOK: true},
		{name: "not github", source: "https://gitlab.com/foo/bar", want: "", wantOK: false},
		{name: "empty", source: "", want: "", wantOK: false},
		{name: "malformed url", source: "not a url at all ://", want: "", wantOK: false},
		{name: "github host with no path names no repo", source: "https://github.com/", want: "", wantOK: false},
		{name: "owner-only path has no repo segment", source: "https://github.com/someorg", want: "", wantOK: false},
		{name: "deep monorepo path collapses to its first two segments", source: "https://github.com/org/monorepo/tree/main/docker/app", want: "https://github.com/org/monorepo/releases", wantOK: true},
		{name: "ssh scheme is rejected, not just a bare ssh remote", source: "ssh://github.com/foo/bar", want: "", wantOK: false},
		{name: "http scheme is normalized to https", source: "http://github.com/foo/bar", want: "https://github.com/foo/bar/releases", wantOK: true},
		{name: "doubled trailing slash is cleaned up", source: "https://github.com/foo/bar//", want: "https://github.com/foo/bar/releases", wantOK: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := githubReleasesURL(c.source)
			require.Equal(t, c.wantOK, ok)
			require.Equal(t, c.want, got)
		})
	}
}

// TestGhcrReleasesURL pins the image-ref-derived half in isolation.
func TestGhcrReleasesURL(t *testing.T) {
	cases := []struct {
		name   string
		image  string
		want   string
		wantOK bool
	}{
		{name: "owner/repo with explicit tag", image: "ghcr.io/jellman86/optimisarr:latest", want: "https://github.com/jellman86/optimisarr/releases", wantOK: true},
		{name: "owner/repo with no tag", image: "ghcr.io/jellman86/optimisarr", want: "https://github.com/jellman86/optimisarr/releases", wantOK: true},
		{name: "owner/repo pinned by digest", image: "ghcr.io/owner/repo@sha256:deadbeef", want: "https://github.com/owner/repo/releases", wantOK: true},
		{name: "extra path segment beyond owner/repo", image: "ghcr.io/owner/repo/extra:1.0", want: "https://github.com/owner/repo/releases", wantOK: true},
		{name: "not a ghcr.io image", image: "docker.io/owner/repo:latest", want: "", wantOK: false},
		{name: "ghcr.io with no repo segment", image: "ghcr.io/owner", want: "", wantOK: false},
		{name: "empty", image: "", want: "", wantOK: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ghcrReleasesURL(c.image)
			require.Equal(t, c.wantOK, ok)
			require.Equal(t, c.want, got)
		})
	}
}
