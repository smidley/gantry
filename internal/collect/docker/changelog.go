package docker

import (
	"net/url"
	"strings"
)

// ociSourceLabel/ociURLLabel are the two OCI image-spec annotations
// (github.com/opencontainers/image-spec) changelogAndProjectURLs reads:
// image.source names the repo the image was built from, image.url names
// the project's own homepage. Neither is Unraid/Community-Applications-
// specific (contrast unraidIconLabel/unraidWebUILabel in docker.go) --
// most well-maintained images carry them regardless of how they were
// installed.
const (
	ociSourceLabel = "org.opencontainers.image.source"
	ociURLLabel    = "org.opencontainers.image.url"
)

// changelogAndProjectURLs derives a container's "see what changed"
// and "see the project" links, in that priority order for changelogURL:
// the org.opencontainers.image.source label when it resolves to a
// github.com repo (githubReleasesURL), else the image ref itself when
// it's a ghcr.io image (ghcrReleasesURL), else no changelog at all.
// projectURL is independent of that chain, not a third rung of it: it's
// always just the org.opencontainers.image.url label verbatim, whether
// or not changelogURL was also derived -- a real box's jellyfin
// container carries both labels at once (github source + jellyfin.org
// project), and both links are worth surfacing together, not one
// suppressing the other.
func changelogAndProjectURLs(labels map[string]string, image string) (changelogURL, projectURL string) {
	if src := labels[ociSourceLabel]; src != "" {
		if cl, ok := githubReleasesURL(src); ok {
			changelogURL = cl
		}
	}
	if changelogURL == "" {
		if cl, ok := ghcrReleasesURL(image); ok {
			changelogURL = cl
		}
	}
	return changelogURL, labels[ociURLLabel]
}

// githubReleasesURL turns an org.opencontainers.image.source label into
// its repo's releases page: ok is false when source isn't a github.com
// URL reached over plain http(s) (some other forge, a non-http(s)
// scheme -- a bare SSH remote fails to parse as a URL at all, but an
// explicit ssh:// one parses fine and still must be rejected -- empty,
// malformed) or names fewer than two path segments (an owner with no
// repo, or no path at all). The output is always rebuilt from scratch
// as "https://github.com/<owner>/<repo>/releases" rather than reusing
// source's own scheme/host/path verbatim: that normalizes an http://
// source to https, and collapsing to exactly the first two non-empty
// path segments both discards any deeper monorepo subpath (a source
// pointing at a subdirectory still names the repo it lives in) and
// tolerates doubled slashes anywhere in the path. A ".git" suffix on
// the repo segment is stripped the same as before.
func githubReleasesURL(source string) (releasesURL string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(source))
	if err != nil {
		return "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", false
	}
	if !strings.EqualFold(u.Host, "github.com") {
		return "", false
	}

	var segments [2]string
	n := 0
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "" {
			continue
		}
		segments[n] = seg
		n++
		if n == 2 {
			break
		}
	}
	if n < 2 {
		return "", false
	}
	owner, repo := segments[0], strings.TrimSuffix(segments[1], ".git")
	if repo == "" {
		return "", false
	}
	return "https://github.com/" + owner + "/" + repo + "/releases", true
}

// ghcrReleasesURL turns a ghcr.io image ref's owner/repo into its
// github.com releases page: ok is false for any non-ghcr.io image, or a
// ghcr.io ref with no repo segment. A tag or digest suffix on the repo
// segment is stripped only when repo is the LAST path segment -- when
// the ref carries extra segments beyond owner/repo, a tag/digest would
// be on the last of those instead, leaving repo itself untouched.
func ghcrReleasesURL(image string) (releasesURL string, ok bool) {
	const prefix = "ghcr.io/"
	if !strings.HasPrefix(image, prefix) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(image, prefix), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	owner, repo := parts[0], parts[1]
	if len(parts) == 2 {
		if i := strings.IndexAny(repo, ":@"); i >= 0 {
			repo = repo[:i]
		}
	}
	if repo == "" {
		return "", false
	}
	return "https://github.com/" + owner + "/" + repo + "/releases", true
}
