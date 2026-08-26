package gpu

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/smidley/gantry/internal/collect/cgroup"
)

// client is one DRM client discovered by a full scan: enough to re-read
// its fdinfo every tick (FDPath) and to bucket its busy-% (Pdev, Owner).
// Owner is resolved once, at discovery — see resolveOwner.
type client struct {
	FDPath string
	PID    int
	Driver string
	Pdev   string
	Owner  string // container name; "" is the host bucket
}

// scanClients walks procRoot/<pid>/fdinfo/<fd> once and returns one entry
// per distinct drm-client-id. pid and fd directory listings are read via
// os.ReadDir, which returns entries sorted by filename, so which fdinfo
// path "wins" a dedupe (the first one encountered for a given client-id)
// is deterministic. A procRoot that can't be listed at all yields an
// empty map rather than an error — Tick treats "no clients found" as a
// normal, if uneventful, scan.
func scanClients(procRoot string) map[string]client {
	out := make(map[string]client)

	pidEntries, err := os.ReadDir(procRoot)
	if err != nil {
		return out
	}
	for _, pidEnt := range pidEntries {
		pid, err := strconv.Atoi(pidEnt.Name())
		if err != nil {
			continue // not a /proc/<pid> directory
		}

		fdinfoDir := filepath.Join(procRoot, pidEnt.Name(), "fdinfo")
		fdEntries, err := os.ReadDir(fdinfoDir)
		if err != nil {
			continue
		}
		for _, fdEnt := range fdEntries {
			path := filepath.Join(fdinfoDir, fdEnt.Name())
			info, ok := readFDInfo(path)
			if !ok || info.ClientID == "" {
				continue
			}
			if _, dup := out[info.ClientID]; dup {
				continue // first fdinfo path seen for this client-id wins
			}
			out[info.ClientID] = client{
				FDPath: path,
				PID:    pid,
				Driver: info.Driver,
				Pdev:   info.Fields["drm-pdev"],
			}
		}
	}
	return out
}

// readFDInfo opens and parses one fdinfo file. ok=false on any open or
// parse failure (file gone, not a DRM fd).
func readFDInfo(path string) (FDInfo, bool) {
	f, err := os.Open(path)
	if err != nil {
		return FDInfo{}, false
	}
	defer f.Close()
	return ParseFDInfo(f)
}

// resolveOwner attributes one pid to a container name via its
// /proc/<pid>/cgroup content, falling back to the host bucket ("") on any
// miss along the way: unreadable cgroup file, no docker id inside it, or
// an id the docker registry doesn't (or no longer) recognize. Real
// host-side DRM clients exist (spike S1) and must land here, not be
// dropped.
func resolveOwner(procRoot string, pid int, lookup func(string) (string, bool)) string {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return ""
	}
	id, ok := cgroup.ContainerID(string(data))
	if !ok {
		return ""
	}
	name, ok := lookup(id)
	if !ok {
		return ""
	}
	return name
}
