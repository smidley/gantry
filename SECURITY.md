# Security Policy

## Supported versions

| Version              | Supported |
| -------------------- | --------- |
| Latest 0.1.x release | Yes       |

Only the latest release (see [CHANGELOG.md](CHANGELOG.md)) receives security
fixes. Older releases do not; upgrade to the latest release.

## Reporting a vulnerability

Do not open a public issue for a security problem.

Report it privately through GitHub:
<https://github.com/smidley/gantry/security/advisories/new>
(the "Report a vulnerability" button on the repository's Security tab).
Include the Gantry version, how it is deployed (Docker, Unraid, or native
binary), and steps to reproduce.

## What to expect

- An acknowledgement within 7 days.
- A fix in the next release, listed in [CHANGELOG.md](CHANGELOG.md), which
  also becomes that version's GitHub Release notes.

## What the project already checks

On every push and pull request, [`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs:

- `govulncheck` via [`scripts/check_go_vulns.py`](scripts/check_go_vulns.py).
  A reachable vulnerability fails CI unless it has a dated, reviewed
  exception in `scripts/go-vuln-exceptions.json`.
- `npm audit --audit-level=high` in `web/`.
- A Trivy scan of the built Docker image. HIGH and CRITICAL CVEs in the
  image's OS packages that already have a fix available fail CI.

Dependabot ([`.github/dependabot.yml`](.github/dependabot.yml)) opens weekly
update pull requests for Go modules, npm packages, and GitHub Actions.
