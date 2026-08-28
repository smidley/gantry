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
// URL at all (some other forge, a bare SSH remote, empty, malformed) or
// names no repo path. Trailing slash and a ".git" suffix are both
// stripped before appending "/releases" -- in either order, since
// stripping the slash first is what exposes ".git" as a suffix at all
// for "owner/repo.git/".
func githubReleasesURL(source string) (releasesURL string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(source))
	if err != nil || u.Scheme == "" || !strings.EqualFold(u.Host, "github.com") {
		return "", false
	}
	path := strings.TrimSuffix(u.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	if path == "" || path == "/" {
		return "", false
	}
	return u.Scheme + "://" + u.Host + path + "/releases", true
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
