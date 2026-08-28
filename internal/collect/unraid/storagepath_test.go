package unraid

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResolveStoragePath is an exhaustive table of every path shape the
// storage panel's mounts can present, per the design in
// docs/superpowers/backlog.md's "Per-container storage panel" sketch:
// array shares (/mnt/user, and the array-only /mnt/user0 alias), cache
// pools (the default "cache" and any custom-named pool -- both only
// recognized via the caller-supplied known-fleet list, never a
// hardcoded literal), individual array disks (/mnt/diskN, purely by
// pattern -- no fleet lookup needed), the flash boot device (/boot,
// fixed, no name), and anything else (a bare mount root with no share
// segment, an Unassigned-Devices-style /mnt/disks path, a pool-shaped
// name the fleet doesn't recognize, or a path under neither /mnt nor
// /boot at all -- e.g. an anonymous docker volume's real on-disk
// location, or an unrelated host bind mount).
func TestResolveStoragePath(t *testing.T) {
	pools := []string{"cache", "rocket_pool"}

	tests := []struct {
		name string
		path string
		want StorageRef
	}{
		{"user share with file", "/mnt/user/appdata/jellyfin/config.db", StorageRef{Kind: "share", Name: "appdata"}},
		{"user share root, no trailing path", "/mnt/user/Movies", StorageRef{Kind: "share", Name: "Movies"}},
		{"user share root, trailing slash", "/mnt/user/Movies/", StorageRef{Kind: "share", Name: "Movies"}},
		{"user0 array-only share", "/mnt/user0/Movies/foo.mkv", StorageRef{Kind: "share", Name: "Movies"}},
		{"user with no share segment", "/mnt/user", StorageRef{Kind: "other"}},
		{"user0 with no share segment", "/mnt/user0/", StorageRef{Kind: "other"}},

		{"default cache pool", "/mnt/cache/appdata/jellyfin", StorageRef{Kind: "pool", Name: "cache"}},
		{"default cache pool, bare root", "/mnt/cache", StorageRef{Kind: "pool", Name: "cache"}},
		{"custom-named pool", "/mnt/rocket_pool/isos/ubuntu.iso", StorageRef{Kind: "pool", Name: "rocket_pool"}},
		{"pool-shaped name not in known fleet", "/mnt/some_other_pool/x", StorageRef{Kind: "other"}},
		{"slot name merely prefixed by a known pool's name", "/mnt/cache2/x", StorageRef{Kind: "other"}},

		{"single-digit array disk", "/mnt/disk1/isos/ubuntu.iso", StorageRef{Kind: "disk", Name: "disk1"}},
		{"double-digit array disk", "/mnt/disk23/backups", StorageRef{Kind: "disk", Name: "disk23"}},
		{"array disk, bare root", "/mnt/disk9", StorageRef{Kind: "disk", Name: "disk9"}},
		{"disk0 is never a real Unraid slot", "/mnt/disk0/x", StorageRef{Kind: "other"}},

		{"flash boot device", "/boot/config/plugins/x.plg", StorageRef{Kind: "flash"}},
		{"flash boot device, bare root", "/boot", StorageRef{Kind: "flash"}},

		{"unassigned-devices style path", "/mnt/disks/sdx1/data", StorageRef{Kind: "other"}},
		{"unassigned-devices remote (SMB/NFS) mount", "/mnt/remotes/server/share", StorageRef{Kind: "other"}},
		{"bare mnt root", "/mnt", StorageRef{Kind: "other"}},
		{"bare mnt root, trailing slash", "/mnt/", StorageRef{Kind: "other"}},
		{"root", "/", StorageRef{Kind: "other"}},
		{"boot-prefixed but not the boot device itself", "/boots", StorageRef{Kind: "other"}},
		{"empty path", "", StorageRef{Kind: "other"}},
		{"docker anonymous volume real location", "/var/lib/docker/volumes/jellyfin_cache/_data", StorageRef{Kind: "other"}},
		{"arbitrary host bind mount", "/home/user/appdata", StorageRef{Kind: "other"}},

		// Unclean paths: filepath.Clean must run before segment-splitting,
		// not after -- a ".." climbing back out of a share into a disk
		// slot, and a doubled slash inside an otherwise-valid share path,
		// must resolve by their post-Clean shape, not their literal one.
		{"dot-dot climbs back out of a user share into a disk slot", "/mnt/user/../disk1/x", StorageRef{Kind: "disk", Name: "disk1"}},
		{"doubled slash within a user share", "/mnt/user//Movies", StorageRef{Kind: "share", Name: "Movies"}},
		{"relative path (no leading slash) never resolves", "mnt/user/appdata", StorageRef{Kind: "other"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ResolveStoragePath(tt.path, pools))
		})
	}
}

// TestResolveStoragePathNoKnownPools pins the pre-first-tick degrade: a
// nil pools list (Collector.Slots() before disks.ini has ever been read)
// must not crash the pool-membership scan, and a cache-shaped path
// simply falls through to "other" rather than being treated as always-
// pool by some hardcoded special case.
func TestResolveStoragePathNoKnownPools(t *testing.T) {
	require.Equal(t, StorageRef{Kind: "other"}, ResolveStoragePath("/mnt/cache/appdata", nil))
}
