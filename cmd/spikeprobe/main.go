// Command spikeprobe verifies Gantry's three day-one access assumptions
// on a real Unraid box (spec §13). Throwaway harness around production
// parsers. Run it inside a container with the Gantry template flags:
//
//	docker run --rm --pid=host --cap-add=SYS_PTRACE \
//	  -v /sys:/host/sys:ro -v /var/local/emhttp:/unraid:ro \
//	  -v /tmp/notifications:/notify \
//	  -v /var/run/docker.sock:/var/run/docker.sock:ro \
//	  <image> -all
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/smidley/gantry/internal/alert"
	"github.com/smidley/gantry/internal/collect/cgroup"
	"github.com/smidley/gantry/internal/collect/gpu"
)

func main() {
	s1 := flag.Bool("s1", false, "S1: foreign-process DRM fdinfo readability")
	s2 := flag.Bool("s2", false, "S2: write a test notification into the unraid spool")
	s3 := flag.Bool("s3", false, "S3: cgroup v2 readability under /host/sys")
	all := flag.Bool("all", false, "run all spikes")
	flag.Parse()

	fail := false
	if *all || *s1 {
		fail = !runS1() || fail
	}
	if *all || *s3 {
		fail = !runS3() || fail
	}
	if *all || *s2 {
		fail = !runS2() || fail
	}
	if fail {
		os.Exit(1)
	}
}

// S1: walk every /proc/<pid>/fdinfo/<fd>; count DRM clients belonging
// to OTHER processes. PASS needs >=1 foreign client (prove SYS_PTRACE
// + pid=host suffices — no privileged mode).
func runS1() bool {
	self := os.Getpid()
	clients, errs := 0, 0
	drivers := map[string]int{}

	pids, _ := filepath.Glob("/proc/[0-9]*")
	for _, pdir := range pids {
		var pid int
		_, _ = fmt.Sscanf(filepath.Base(pdir), "%d", &pid)
		if pid == self {
			continue
		}
		fds, err := os.ReadDir(filepath.Join(pdir, "fdinfo"))
		if err != nil {
			errs++
			continue
		}
		for _, fd := range fds {
			f, err := os.Open(filepath.Join(pdir, "fdinfo", fd.Name()))
			if err != nil {
				continue
			}
			info, ok := gpu.ParseFDInfo(f)
			_ = f.Close()
			if !ok {
				continue
			}
			clients++
			drivers[info.Driver]++
			if clients <= 3 { // dump a few raw for fixture capture
				fmt.Printf("--- raw fdinfo (pid %d, driver %s) ---\n", pid, info.Driver)
				for k, v := range info.Fields {
					fmt.Printf("%s: %s\n", k, v)
				}
				if raw, err := os.ReadFile(filepath.Join(pdir, "cgroup")); err == nil {
					id, okc := cgroup.ContainerID(string(raw))
					fmt.Printf("cgroup container: %v %s\n", okc, id)
				}
			}
		}
	}
	if clients > 0 {
		fmt.Printf("S1 PASS: %d foreign DRM clients readable (drivers: %v; %d pids unreadable)\n", clients, drivers, errs)
		return true
	}
	fmt.Printf("S1 FAIL: no foreign DRM clients readable (%d pids unreadable) — is a GPU workload running? does the container have pid=host + SYS_PTRACE?\n", errs)
	return false
}

// S2: drop a test notification into the mounted spool. Human verifies
// it appears in the Unraid GUI / configured agents.
func runS2() bool {
	path, err := alert.WriteNotify("/notify", alert.Notification{
		Event:       "Gantry",
		Subject:     "Gantry spike S2",
		Description: "If you can read this in the Unraid GUI or your notification agent, S2 passes.",
		Importance:  "normal",
	}, time.Now())
	if err != nil {
		fmt.Printf("S2 FAIL: %v (is /tmp/notifications mounted rw at /notify?)\n", err)
		return false
	}
	fmt.Printf("S2 WROTE: %s — now confirm it shows in the Unraid GUI/agents (human step)\n", path)
	return true
}

// S3: find docker container cgroup dirs under /host/sys/fs/cgroup and
// read one cpu.stat.
func runS3() bool {
	root := "/host/sys/fs/cgroup"
	if _, err := os.Stat(filepath.Join(root, "cgroup.controllers")); err != nil {
		fmt.Printf("S3 FAIL: %s/cgroup.controllers not readable (%v) — cgroup v1 box or mount missing; docker stats API fallback will be used\n", root, err)
		return false
	}
	dirs, _ := filepath.Glob(filepath.Join(root, "docker", "*", "cpu.stat"))
	if len(dirs) == 0 {
		fmt.Printf("S3 FAIL: no docker/*/cpu.stat under %s — dump of %s/docker follows\n", root, root)
		entries, _ := os.ReadDir(filepath.Join(root, "docker"))
		for _, e := range entries {
			fmt.Println("  ", e.Name())
		}
		return false
	}
	body, err := os.ReadFile(dirs[0])
	if err != nil {
		fmt.Printf("S3 FAIL: found %d container cgroups but cpu.stat unreadable: %v\n", len(dirs), err)
		return false
	}
	fmt.Printf("S3 PASS: %d container cgroups; sample cpu.stat:\n%s", len(dirs), string(body))

	// PSI readability — the enabler for the Insights engine (spec §16).
	// Informational: S3 still passes without it, but record the verdict.
	dir := filepath.Dir(dirs[0])
	for _, name := range []string{"io.pressure", "cpu.pressure", "memory.pressure"} {
		if p, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			fmt.Printf("S3 PSI: container %s readable:\n%s", name, string(p))
		} else {
			fmt.Printf("S3 PSI: container %s NOT readable (%v) — insights degrade to correlation-only\n", name, err)
		}
	}
	if p, err := os.ReadFile("/proc/pressure/io"); err == nil {
		fmt.Printf("S3 PSI: host /proc/pressure/io readable:\n%s", string(p))
	} else {
		fmt.Printf("S3 PSI: host /proc/pressure/io NOT readable (%v)\n", err)
	}
	return true
}
