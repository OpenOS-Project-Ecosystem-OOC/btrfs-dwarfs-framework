// Package core — bdfs.go
// Integration with btrfs-dwarfs-framework (bdfs) for archiving old frzr deployments
// as compressed DwarFS images before they are deleted.
//
// bdfs CLI is optional: if the binary is absent or returns an error, the hook
// is skipped silently so that frzr continues to work on systems without bdfs.

package core

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// BdfsConfig holds the subset of bdfs configuration relevant to frzr.
// Values are read from /etc/bdfs/bdfs.conf (INI format) when present;
// defaults are used otherwise.
type BdfsConfig struct {
	// ArchiveDir is the directory where DwarFS archives are written.
	// Corresponds to [archive] dir in bdfs.conf.
	ArchiveDir string

	// BdfsBin is the path to the bdfs CLI binary.
	BdfsBin string

	// Enabled controls whether the bdfs hook runs at all.
	Enabled bool
}

// DefaultBdfsConfig returns a BdfsConfig with safe defaults.
func DefaultBdfsConfig() BdfsConfig {
	return BdfsConfig{
		ArchiveDir: "/var/lib/bdfs/archives/frzr",
		BdfsBin:    "bdfs",
		Enabled:    true,
	}
}

// LoadBdfsConfig reads /etc/bdfs/bdfs.conf and overlays values onto defaults.
// Missing file or parse errors are non-fatal; defaults are returned.
func LoadBdfsConfig() BdfsConfig {
	cfg := DefaultBdfsConfig()

	data, err := os.ReadFile("/etc/bdfs/bdfs.conf")
	if err != nil {
		// No config file — use defaults.
		return cfg
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "archive_dir":
			cfg.ArchiveDir = val
		case "bdfs_bin":
			cfg.BdfsBin = val
		case "enabled":
			cfg.Enabled = val == "1" || strings.EqualFold(val, "true") || strings.EqualFold(val, "yes")
		}
	}

	return cfg
}

// BdfsArchiveResult is returned by BdfsArchiveDeployment.
type BdfsArchiveResult struct {
	// ArchivePath is the path of the created DwarFS image, empty on failure.
	ArchivePath string `json:"archive_path"`
	// Skipped is true when bdfs is not available or not enabled.
	Skipped bool `json:"skipped"`
	// Error holds a human-readable error string when archival failed.
	Error string `json:"error,omitempty"`
}

// BdfsArchiveDeployment archives the BTRFS subvolume at subvolPath as a
// DwarFS image in cfg.ArchiveDir, naming it after deploymentName and the
// current UTC timestamp.
//
// Returns BdfsArchiveResult. Callers should treat Skipped=true as a
// non-fatal condition (bdfs not installed or disabled).
func BdfsArchiveDeployment(cfg BdfsConfig, subvolPath, deploymentName string) BdfsArchiveResult {
	if !cfg.Enabled {
		return BdfsArchiveResult{Skipped: true}
	}

	// Verify bdfs binary is reachable.
	if _, err := exec.LookPath(cfg.BdfsBin); err != nil {
		return BdfsArchiveResult{Skipped: true}
	}

	if err := os.MkdirAll(cfg.ArchiveDir, 0o755); err != nil {
		return BdfsArchiveResult{Error: fmt.Sprintf("mkdir archive dir: %v", err)}
	}

	ts := time.Now().UTC().Format("20060102T150405Z")
	archiveName := fmt.Sprintf("%s_%s.dwarfs", sanitizeName(deploymentName), ts)
	archivePath := filepath.Join(cfg.ArchiveDir, archiveName)

	// bdfs snapshot demote --to-dwarfs <subvol> --output <archive>
	cmd := exec.Command(cfg.BdfsBin, "snapshot", "demote",
		"--to-dwarfs", subvolPath,
		"--output", archivePath,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return BdfsArchiveResult{Error: fmt.Sprintf("bdfs demote: %v", err)}
	}

	return BdfsArchiveResult{ArchivePath: archivePath}
}

// BdfsListArchives returns the DwarFS archives in cfg.ArchiveDir that match
// the given deploymentName prefix. Returns nil on any error.
func BdfsListArchives(cfg BdfsConfig, deploymentName string) []string {
	entries, err := os.ReadDir(cfg.ArchiveDir)
	if err != nil {
		return nil
	}

	prefix := sanitizeName(deploymentName) + "_"
	var matches []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".dwarfs") {
			matches = append(matches, filepath.Join(cfg.ArchiveDir, e.Name()))
		}
	}
	return matches
}

// sanitizeName replaces characters that are unsafe in filenames with underscores.
func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// bdfsResultJSON is a helper used in tests to serialise BdfsArchiveResult.
func bdfsResultJSON(r BdfsArchiveResult) string {
	b, _ := json.Marshal(r)
	return string(b)
}
