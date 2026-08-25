package cgroup

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContainerID(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
		ok      bool
	}{
		{
			name:    "cgroup v2 unraid",
			content: "0::/docker/8f2f3a1b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f7\n",
			want:    "8f2f3a1b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f7",
			ok:      true,
		},
		{
			name: "cgroup v1 multiline",
			content: "12:pids:/docker/abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd\n" +
				"11:memory:/docker/abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd\n",
			want: "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd",
			ok:   true,
		},
		{
			name:    "host process",
			content: "0::/init.scope\n",
			ok:      false,
		},
		{
			name:    "cgroup v2 systemd-style scope",
			content: "0::/system.slice/docker-1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef.scope\n",
			want:    "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
			ok:      true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ContainerID(c.content)
			require.Equal(t, c.ok, ok)
			require.Equal(t, c.want, got)
		})
	}
}
