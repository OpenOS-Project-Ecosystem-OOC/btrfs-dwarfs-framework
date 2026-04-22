package settings

/*	License: GPLv3
	Authors:
		OpenOS Project Contributors
	Copyright: 2024
	Description:
		Configuration for frzr-meta-root. Merges ABRoot v2's abroot.json
		schema with frzr's source file, and adds Incus-specific fields that
		replace the Docker/Podman registry configuration.
*/

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config holds all runtime configuration for frzr-meta-root.
// It is loaded from one of the standard config file locations (see Load).
type Config struct {
	// --- Incus backend (replaces Docker/Podman registry fields) ---

	// IncusSocket is the path to the Incus unix socket.
	// Default: /var/lib/incus/unix.socket
	IncusSocket string `json:"incusSocket"`

	// IncusRemote is the name of the pre-configured Incus OCI remote.
	// Set up with: incus remote add <name> <url> --protocol=oci
	// Default: oci-registry
	IncusRemote string `json:"incusRemote"`

	// IncusStoragePool is the Incus storage pool used for temporary build containers.
	// Default: default
	IncusStoragePool string `json:"incusStoragePool"`

	// --- Image reference (replaces ABRoot registry/name/tag + frzr REPO:CHANNEL) ---

	// Name is the OCI image name, e.g. "myorg/myos".
	Name string `json:"name"`

	// Tag is the OCI image tag / frzr channel, e.g. "stable", "testing", "main".
	Tag string `json:"tag"`

	// MaxParallelDownloads controls layer download concurrency (passed to Incus).
	MaxParallelDownloads int `json:"maxParallelDownloads"`

	// --- Deploy mode ---

	// DeployMode selects the deployment strategy:
	//   "ab"    — A/B partition transactions (ABRoot model, default)
	//   "btrfs" — btrfs subvolume deployments (frzr model)
	DeployMode string `json:"deployMode"`

	// --- A/B partition labels (used when DeployMode = "ab") ---

	PartLabelA    string `json:"partLabelA"`
	PartLabelB    string `json:"partLabelB"`
	PartLabelVar  string `json:"partLabelVar"`
	PartLabelBoot string `json:"partLabelBoot"`
	PartLabelEfi  string `json:"partLabelEfi"`
	PartCryptVar  string `json:"partCryptVar"`

	// ThinProvisioning enables LVM thin-provisioning layout (ABRoot model).
	ThinProvisioning bool   `json:"thinProvisioning"`
	ThinInitVolume   string `json:"thinInitVolume"`

	// --- btrfs deploy paths (used when DeployMode = "btrfs") ---

	// BtrfsMountLabel is the filesystem label of the btrfs root volume.
	// Default: frzr_root (matches frzr convention)
	BtrfsMountLabel string `json:"btrfsMountLabel"`

	// BtrfsEfiLabel is the filesystem label of the EFI partition.
	// Default: frzr_efi (matches frzr convention)
	BtrfsEfiLabel string `json:"btrfsEfiLabel"`

	// --- Package manager (used when DeployMode = "ab") ---

	// iPkgMng* fields mirror ABRoot's package manager configuration.
	IPkgMngPre    string `json:"iPkgMngPre"`
	IPkgMngPost   string `json:"iPkgMngPost"`
	IPkgMngAdd    string `json:"iPkgMngAdd"`
	IPkgMngRm     string `json:"iPkgMngRm"`
	IPkgMngApi    string `json:"iPkgMngApi"`
	IPkgMngStatus int    `json:"iPkgMngStatus"`

	// --- System commands ---

	UpdateInitramfsCmd string `json:"updateInitramfsCmd"`
	UpdateGrubCmd      string `json:"updateGrubCmd"`

	// --- Differ API (image diff service, optional) ---
	DifferURL string `json:"differURL"`
}

// Cnf is the global config instance populated by Load.
var Cnf *Config

// configSearchPaths lists locations checked in order. First match wins.
var configSearchPaths = []string{
	"~/.config/frzr-meta-root/frzr-meta-root.json",
	"config/frzr-meta-root.json",
	"../config/frzr-meta-root.json",
	"/etc/frzr-meta-root/frzr-meta-root.json",
	"/usr/share/frzr-meta-root/frzr-meta-root.json",
}

// defaults returns a Config with sensible defaults so that minimal config
// files only need to specify the fields that differ from these values.
func defaults() *Config {
	return &Config{
		IncusSocket:          "/var/lib/incus/unix.socket",
		IncusRemote:          "oci-registry",
		IncusStoragePool:     "default",
		MaxParallelDownloads: 2,
		DeployMode:           "ab",
		PartLabelA:           "fmr-a",
		PartLabelB:           "fmr-b",
		PartLabelVar:         "fmr-var",
		PartLabelBoot:        "fmr-boot",
		PartLabelEfi:         "fmr-efi",
		BtrfsMountLabel:      "frzr_root",
		BtrfsEfiLabel:        "frzr_efi",
		IPkgMngStatus:        0,
	}
}

// Load reads the first config file found in configSearchPaths and populates Cnf.
// Returns an error if no config file is found or if parsing fails.
func Load() error {
	for _, path := range configSearchPaths {
		expanded := expandHome(path)
		data, err := os.ReadFile(expanded)
		if err != nil {
			continue
		}

		cfg := defaults()
		if err := json.Unmarshal(data, cfg); err != nil {
			return fmt.Errorf("parsing config %s: %w", expanded, err)
		}

		if err := cfg.validate(); err != nil {
			return fmt.Errorf("invalid config %s: %w", expanded, err)
		}

		Cnf = cfg
		return nil
	}

	return fmt.Errorf("no config file found; checked: %v", configSearchPaths)
}

// validate checks that required fields are set and that DeployMode is valid.
func (c *Config) validate() error {
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if c.Tag == "" {
		return fmt.Errorf("tag is required")
	}
	if c.DeployMode != "ab" && c.DeployMode != "btrfs" {
		return fmt.Errorf("deployMode must be \"ab\" or \"btrfs\", got %q", c.DeployMode)
	}
	return nil
}

func expandHome(path string) string {
	if len(path) > 1 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return home + path[1:]
	}
	return path
}
