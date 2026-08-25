// Package cgroup maps host PIDs to docker container IDs via /proc/<pid>/cgroup.
package cgroup

import "regexp"

var idRe = regexp.MustCompile(`(?:docker[/-])([0-9a-f]{64})`)

func ContainerID(content string) (string, bool) {
	m := idRe.FindStringSubmatch(content)
	if m == nil {
		return "", false
	}
	return m[1], true
}
