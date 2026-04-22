module github.com/openos-project/frzr-meta-root

go 1.25

require (
	github.com/lxc/incus/v6 v6.8.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/spf13/cobra v1.10.2
	github.com/google/uuid v1.6.0
)

// Deps to add when ABRoot v2 core files are ported (no OCI dependency):
//   github.com/linux-immutability-tools/EtcBuilder v1.4.0
//   github.com/spf13/viper v1.21.0
//   github.com/pterm/pterm v0.12.82
//   github.com/dustin/go-humanize v1.0.1
//   github.com/hashicorp/go-version v1.8.0
//   github.com/shirou/gopsutil v3.21.11+incompatible
//   github.com/vanilla-os/orchid v0.6.1
//   golang.org/x/sys v0.39.0
//
// Removed from ABRoot v2 (replaced by Incus):
//   github.com/containers/buildah, go.podman.io/*, github.com/docker/docker,
//   github.com/fsouza/go-dockerclient, github.com/vanilla-os/prometheus,
//   github.com/moby/buildkit
