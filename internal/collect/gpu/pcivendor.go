package gpu

import (
	"os"
	"path/filepath"
	"strings"
)

// pciVendorNames maps a PCI vendor ID, as found in
// sysRoot/bus/pci/devices/<pdev>/vendor (a single hex line, e.g.
// "0x8086\n"), to a short human-readable name -- Intel/NVIDIA/AMD are
// the only ones the GPU card title (walker.go's own EntityMeta) or the
// Nvidia presence probe (nvidia.go) ever need to distinguish.
var pciVendorNames = map[string]string{
	"0x8086": "Intel",
	"0x10de": "NVIDIA",
	"0x1002": "AMD",
}

// nvidiaVendorID is 0x10de -- NvidiaCollector's own Probe scans for this
// specifically (see hasPCIVendor's doc).
const nvidiaVendorID = "0x10de"

// readPCIVendor reads sysRoot/bus/pci/devices/<pdev>/vendor and returns
// it trimmed, or "" on any error -- a missing pdev directory (no
// /host/sys mount, or a synthetic entity id like "gpu0" that was never a
// real PCI address) is the expected common case, not something worth
// distinguishing from a genuinely absent vendor file.
func readPCIVendor(sysRoot, pdev string) string {
	data, err := os.ReadFile(filepath.Join(sysRoot, "bus/pci/devices", pdev, "vendor"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// vendorNameForPdev maps one PCI device's vendor ID to its display name,
// falling back to the generic "GPU" label for an unreadable vendor file
// (permissions, no /host/sys mount, or the "gpu0" synthetic id) or an ID
// this app doesn't specifically recognize -- see EntityMeta's own doc
// for where this feeds the GPU card title.
func vendorNameForPdev(sysRoot, pdev string) string {
	if name, ok := pciVendorNames[readPCIVendor(sysRoot, pdev)]; ok {
		return name
	}
	return "GPU"
}

// hasPCIVendor scans every device under sysRoot/bus/pci/devices for one
// whose vendor file matches vendorID -- NvidiaCollector's own Probe uses
// this (vendorID = nvidiaVendorID) to tell "no Nvidia GPU on this box at
// all" (Status.NotApplicable -- nothing to fix, so the SourcesBanner
// hint would just be noise) apart from "Nvidia GPU present but
// nvidia-smi isn't" (today's existing, actionable hint). A sysRoot that
// can't be listed at all (no /host/sys mount) reports false rather than
// erroring -- Probe's own PATH check already covers "this box has no
// working Nvidia integration at all" regardless of which reason applies.
func hasPCIVendor(sysRoot, vendorID string) bool {
	entries, err := os.ReadDir(filepath.Join(sysRoot, "bus/pci/devices"))
	if err != nil {
		return false
	}
	for _, ent := range entries {
		if readPCIVendor(sysRoot, ent.Name()) == vendorID {
			return true
		}
	}
	return false
}
