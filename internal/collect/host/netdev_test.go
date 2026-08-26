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

	all, err := ParseNetDev(f)
	require.NoError(t, err)
	require.Equal(t, IfCounters{RxBytes: 111111, TxBytes: 111111}, all["lo"])
	require.Equal(t, IfCounters{RxBytes: 500000000, TxBytes: 100000000}, all["eth0"])
	require.Equal(t, IfCounters{RxBytes: 2000, TxBytes: 3000}, all["vethaaaa11"])
	require.Equal(t, IfCounters{RxBytes: 2500, TxBytes: 3500}, all["vethbbbb22"])
	require.Equal(t, IfCounters{RxBytes: 9000, TxBytes: 8000}, all["docker0"])
}

func TestFilteredIfacesDropsVirtualInterfaces(t *testing.T) {
	all := map[string]IfCounters{
		"lo":         {RxBytes: 1, TxBytes: 1},
		"eth0":       {RxBytes: 500000000, TxBytes: 100000000},
		"vethaaaa11": {RxBytes: 2000, TxBytes: 3000},
		"vethbbbb22": {RxBytes: 2500, TxBytes: 3500},
		"docker0":    {RxBytes: 9000, TxBytes: 8000},
		"virbr0":     {RxBytes: 10, TxBytes: 10},
		"br-abc123":  {RxBytes: 10, TxBytes: 10},
		"tap0":       {RxBytes: 10, TxBytes: 10},
	}
	filtered := filteredIfaces(all)
	require.Equal(t, map[string]IfCounters{"eth0": {RxBytes: 500000000, TxBytes: 100000000}}, filtered)
}
