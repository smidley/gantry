package host

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseNetDev(t *testing.T) {
	f, err := os.Open("testdata/net_dev.txt")
	require.NoError(t, err)
	defer f.Close()

	all, err := parseNetDev(f)
	require.NoError(t, err)
	require.Equal(t, ifCounters{rxBytes: 111111, txBytes: 111111}, all["lo"])
	require.Equal(t, ifCounters{rxBytes: 500000000, txBytes: 100000000}, all["eth0"])
	require.Equal(t, ifCounters{rxBytes: 2000, txBytes: 3000}, all["vethaaaa11"])
	require.Equal(t, ifCounters{rxBytes: 2500, txBytes: 3500}, all["vethbbbb22"])
	require.Equal(t, ifCounters{rxBytes: 9000, txBytes: 8000}, all["docker0"])
}

func TestFilteredIfacesDropsVirtualInterfaces(t *testing.T) {
	all := map[string]ifCounters{
		"lo":         {rxBytes: 1, txBytes: 1},
		"eth0":       {rxBytes: 500000000, txBytes: 100000000},
		"vethaaaa11": {rxBytes: 2000, txBytes: 3000},
		"vethbbbb22": {rxBytes: 2500, txBytes: 3500},
		"docker0":    {rxBytes: 9000, txBytes: 8000},
		"virbr0":     {rxBytes: 10, txBytes: 10},
		"br-abc123":  {rxBytes: 10, txBytes: 10},
		"tap0":       {rxBytes: 10, txBytes: 10},
	}
	filtered := filteredIfaces(all)
	require.Equal(t, map[string]ifCounters{"eth0": {rxBytes: 500000000, txBytes: 100000000}}, filtered)
}
