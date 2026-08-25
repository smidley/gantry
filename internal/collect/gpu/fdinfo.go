// Package gpu reads per-process GPU accounting from DRM fdinfo,
// the mechanism behind Gantry's per-container Intel/AMD GPU stats.
package gpu

import (
	"bufio"
	"io"
	"strings"
)

type FDInfo struct {
	Driver   string
	ClientID string
	Fields   map[string]string
}

// ParseFDInfo scans one fdinfo file. Lines are "key:\tvalue"; only
// drm-* keys are retained. ok=false when no drm-driver line is present.
func ParseFDInfo(r io.Reader) (FDInfo, bool) {
	info := FDInfo{Fields: make(map[string]string)}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		k, v, found := strings.Cut(line, ":")
		if !found || !strings.HasPrefix(k, "drm-") {
			continue
		}
		v = strings.TrimSpace(v)
		info.Fields[k] = v
		switch k {
		case "drm-driver":
			info.Driver = v
		case "drm-client-id":
			info.ClientID = v
		}
	}
	if info.Driver == "" {
		return FDInfo{}, false
	}
	return info, true
}
