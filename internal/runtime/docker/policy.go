// Package docker will run sessions in containers (M5). Until then it holds the policy compiler, so the
// config-to-container mapping and its hardening floor are tested before any container is started.
package docker

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"time"

	"github.com/JanDeDobbeleer/mint/internal/core"
)

// RunSpec is the part of `docker run` that the policy decides. The runner adds the image, the workspace volume,
// the per-run agent home mount, labels and networks; repository-provided runArgs and mounts are never honoured.
type RunSpec struct {
	Args []string
	// ProxyAllowedHosts and ProxyBlockedHosts configure the egress proxy sidecar. Blocked entries win.
	ProxyAllowedHosts []string
	ProxyBlockedHosts []string
	WallClock         time.Duration
	StallTimeout      time.Duration
}

// Defaults applied when the policy leaves a limit unset: containers are always bounded.
const (
	defaultPIDs     = 512
	defaultMemoryMB = 4096
)

// Compile maps a generic policy to container flags. The hardening floor (all capabilities dropped,
// no-new-privileges, read-only root, tmpfs scratch) is not configurable. The policy can only add scratch
// space and tighten limits. Host paths are never mounted: filesystem entries must be absolute container paths
// and become tmpfs; read-only host paths and credential injection are not supported in v1.
func Compile(p core.Policy) (RunSpec, error) {
	if len(p.Filesystem.ReadOnly) > 0 {
		return RunSpec{}, errors.New("read-only paths are not supported by the container runtime: the root filesystem is already read-only and host paths are never mounted")
	}
	if len(p.Credentials) > 0 {
		return RunSpec{}, errors.New("credential injection is not supported by the container runtime in v1: no GitHub credential enters a session")
	}
	pids, memoryMB := p.Limits.PIDs, int(p.Limits.MemoryBytes>>20)
	if pids == 0 {
		pids = defaultPIDs
	}
	if memoryMB == 0 {
		memoryMB = defaultMemoryMB
	}
	args := []string{
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--read-only",
		"--pids-limit=" + strconv.Itoa(pids),
		"--memory=" + strconv.Itoa(memoryMB) + "m",
		"--tmpfs=/tmp:rw,noexec,nosuid,size=256m",
	}
	if p.Limits.CPUs > 0 {
		args = append(args, "--cpus="+strconv.FormatFloat(p.Limits.CPUs, 'f', -1, 64))
	}
	for _, dir := range slices.Sorted(slices.Values(p.Filesystem.ReadWrite)) {
		if !path.IsAbs(dir) || path.Clean(dir) != dir || dir == "/" {
			return RunSpec{}, fmt.Errorf("scratch path %q must be a clean absolute container path", dir)
		}
		if dir == "/tmp" {
			continue
		}
		args = append(args, "--tmpfs="+dir+":rw,nosuid,size=256m")
	}
	return RunSpec{
		Args:              args,
		ProxyAllowedHosts: slices.Sorted(slices.Values(p.Network.AllowedHosts)),
		ProxyBlockedHosts: slices.Sorted(slices.Values(p.Network.BlockedHosts)),
		WallClock:         p.Limits.WallClock,
		StallTimeout:      p.Limits.StallTimeout,
	}, nil
}
