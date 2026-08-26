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
		{
			name:    "private cgroupns relativized one level up",
			content: "0::/../0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n",
			want:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			ok:      true,
		},
		{
			name:    "private cgroupns relativized with no leading dots",
			content: "0::/abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789\n",
			want:    "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
			ok:      true,
		},
		{
			name:    "private cgroupns relativized two levels up",
			content: "0::/../../fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210\n",
			want:    "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
			ok:      true,
		},
		{
			name:    "63 hex chars is not a valid id",
			content: "0::/../0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde\n",
			ok:      false,
		},
		{
			name:    "64 chars mixed case is not a valid id",
			content: "0::/../0123456789Abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n",
			ok:      false,
		},
		{
			name:    "64 hex chars as substring of a longer alnum segment",
			content: "0::/../0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdefg\n",
			ok:      false,
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
