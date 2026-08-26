// Package cgroup maps host PIDs to docker container IDs via /proc/<pid>/cgroup.
package cgroup

import "regexp"

var idRe = regexp.MustCompile(`(?:docker[/-])([0-9a-f]{64})`)

var bareIDRe = regexp.MustCompile(`/([0-9a-f]{64})(?:/|\n|$)`)

func ContainerID(content string) (string, bool) {
	if m := idRe.FindStringSubmatch(content); m != nil {
		return m[1], true
	}
	if m := bareIDRe.FindStringSubmatch(content); m != nil {
		return m[1], true
	}
	return "", false
}
