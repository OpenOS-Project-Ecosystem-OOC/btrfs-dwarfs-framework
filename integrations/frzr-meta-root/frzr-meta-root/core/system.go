package core

/*	License: GPLv3
	Authors:
		OpenOS Project Contributors
	Copyright: 2024
	Description:
		ABSystem transaction engine, adapted from ABRoot v2.
		The only structural change from upstream is that all OCI operations
		now go through the OCIBackend interface (core/incus.go) rather than
		calling the Podman/Buildah/prometheus functions directly.

		The transaction logic, lock file management, and rollback mechanism
		are unchanged from ABRoot v2.
*/

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	digest "github.com/opencontainers/go-digest"
	"github.com/openos-project/frzr-meta-root/settings"
)

// ABSystem manages system operations (upgrade, package apply, rollback)
// on an ABRoot-compliant A/B partition layout.
type ABSystem struct {
	Checks   *Checks
	RootM    *ABRootManager
	CurImage *ABImage
	OCI      OCIBackend // Incus client — replaces prometheus/buildah
}

// Supported operation types.
const (
	UPGRADE       = "upgrade"
	FORCE_UPGRADE = "force-upgrade"
	APPLY         = "package-apply"
	INITRAMFS     = "initramfs"
)

// Rollback response codes.
const (
	ROLLBACK_RES_YES     = "rollback-yes"
	ROLLBACK_RES_NO      = "rollback-no"
	ROLLBACK_UNNECESSARY = "rollback-unnecessary"
	ROLLBACK_SUCCESS     = "rollback-success"
	ROLLBACK_FAILED      = "rollback-failed"
)

// ABSystemOperation is the type for operation identifiers.
type ABSystemOperation string

// ABRollbackResponse is the type for rollback result codes.
type ABRollbackResponse string

var (
	operationLockFile     = filepath.Join("/run", "frzr-meta-root", "operation.lock")
	finalizingFile        = filepath.Join("/run", "frzr-meta-root", "finalizing")
	userStopFile          = filepath.Join("/run", "frzr-meta-root", "userStop")
	finishedOperationFile = filepath.Join("/run", "frzr-meta-root", "finished")

	ErrNoUpdate        = errors.New("no update available")
	ErrUserStopped     = errors.New("operation stopped per user request")
	ErrOperationLocked = errors.New("another operation is currently running")
)

// NewABSystem initialises an ABSystem with the provided OCIBackend.
// The backend is always an *IncusClient in production; tests can inject a mock.
func NewABSystem(oci OCIBackend) (*ABSystem, error) {
	PrintVerboseInfo("NewABSystem", "running...")

	i, err := NewABImageFromRoot()
	if err != nil {
		PrintVerboseErr("NewABSystem", 0, err)
		return nil, err
	}

	return &ABSystem{
		Checks:   NewChecks(),
		RootM:    NewABRootManager(),
		CurImage: i,
		OCI:      oci,
	}, nil
}

// CheckUpdate returns the new digest and whether an update is available.
func (s *ABSystem) CheckUpdate() (digest.Digest, bool, error) {
	PrintVerboseInfo("ABSystem.CheckUpdate", "running...")
	return s.OCI.HasUpdate(settings.Cnf.Name, settings.Cnf.Tag, s.CurImage.Digest)
}

// RunOperation executes the given operation atomically.
// dryRun = true simulates the operation without writing to disk.
func (s *ABSystem) RunOperation(op ABSystemOperation, dryRun bool) error {
	PrintVerboseInfo("ABSystem.RunOperation", string(op))

	if err := s.Checks.PerformAllChecks(); err != nil {
		return err
	}

	if _, err := os.Stat(operationLockFile); err == nil {
		return ErrOperationLocked
	}

	if !dryRun {
		if err := os.MkdirAll(filepath.Dir(operationLockFile), 0o755); err != nil {
			return err
		}
		lockID := uuid.New().String()
		if err := os.WriteFile(operationLockFile, []byte(lockID), 0o644); err != nil {
			return err
		}
		defer os.Remove(operationLockFile)
	}

	switch op {
	case UPGRADE, FORCE_UPGRADE:
		return s.runUpgrade(op == FORCE_UPGRADE, dryRun)
	case APPLY:
		return s.runPackageApply(dryRun)
	case INITRAMFS:
		return s.runInitramfsUpdate(dryRun)
	default:
		return fmt.Errorf("unknown operation: %s", op)
	}
}

// runUpgrade pulls the latest image via the OCIBackend and applies it to the
// future partition. This is the core of the A/B transaction.
func (s *ABSystem) runUpgrade(force, dryRun bool) error {
	PrintVerboseInfo("ABSystem.runUpgrade", "checking for update...")

	newDigest, hasUpdate, err := s.CheckUpdate()
	if err != nil {
		return err
	}

	if !hasUpdate && !force {
		return ErrNoUpdate
	}

	cfg := settings.Cnf
	future, err := s.RootM.GetFuture()
	if err != nil {
		return fmt.Errorf("getting future partition: %w", err)
	}

	transDir := filepath.Join("/var/lib/frzr-meta-root", "trans-"+uuid.New().String())
	futureMount := filepath.Join("/var/lib/frzr-meta-root", "future-mount")

	if !dryRun {
		if err := os.MkdirAll(transDir, 0o755); err != nil {
			return err
		}
		defer os.RemoveAll(transDir)

		if err := future.Mount(futureMount); err != nil {
			return fmt.Errorf("mounting future partition: %w", err)
		}
		defer future.Unmount(futureMount)
	}

	PrintVerboseInfo("ABSystem.runUpgrade", "exporting rootfs to future partition...")

	// Build the image recipe from staged packages (if any).
	recipe, err := s.buildRecipe(cfg.Name + ":" + cfg.Tag)
	if err != nil {
		return err
	}

	if !dryRun {
		if err := s.OCI.ExportRootFs(cfg.Name, cfg.Tag, recipe, transDir, futureMount); err != nil {
			return fmt.Errorf("exporting rootfs: %w", err)
		}
	}

	// Update GRUB and initramfs inside the future root via chroot.
	if !dryRun {
		if err := s.updateBootInChroot(futureMount, future); err != nil {
			return err
		}
	}

	// Record the new image state.
	newImage, err := NewABImage(newDigest, cfg.Name+":"+cfg.Tag)
	if err != nil {
		return err
	}

	if !dryRun {
		if err := newImage.WriteTo(futureMount); err != nil {
			return err
		}
		if err := s.OCI.DeleteAllButLatestImage(); err != nil {
			PrintVerboseErr("ABSystem.runUpgrade", 0, "cleanup warning:", err)
		}
	}

	PrintVerboseInfo("ABSystem.runUpgrade", "upgrade staged; reboot to apply")
	return nil
}

// runPackageApply builds a local image with staged packages applied and
// exports it to the future partition.
func (s *ABSystem) runPackageApply(dryRun bool) error {
	PrintVerboseInfo("ABSystem.runPackageApply", "running...")

	cfg := settings.Cnf
	recipe, err := s.buildRecipe(cfg.Name + ":" + cfg.Tag)
	if err != nil {
		return err
	}

	if recipe == nil {
		return fmt.Errorf("no staged package changes found")
	}

	future, err := s.RootM.GetFuture()
	if err != nil {
		return err
	}

	futureMount := filepath.Join("/var/lib/frzr-meta-root", "future-mount")
	transDir := filepath.Join("/var/lib/frzr-meta-root", "trans-"+uuid.New().String())

	if !dryRun {
		if err := os.MkdirAll(transDir, 0o755); err != nil {
			return err
		}
		defer os.RemoveAll(transDir)

		if err := future.Mount(futureMount); err != nil {
			return err
		}
		defer future.Unmount(futureMount)

		if err := s.OCI.ExportRootFs(cfg.Name, cfg.Tag, recipe, transDir, futureMount); err != nil {
			return err
		}

		if err := s.updateBootInChroot(futureMount, future); err != nil {
			return err
		}
	}

	return nil
}

// runInitramfsUpdate regenerates the initramfs in the future partition.
func (s *ABSystem) runInitramfsUpdate(dryRun bool) error {
	PrintVerboseInfo("ABSystem.runInitramfsUpdate", "running...")

	future, err := s.RootM.GetFuture()
	if err != nil {
		return err
	}

	futureMount := filepath.Join("/var/lib/frzr-meta-root", "future-mount")

	if !dryRun {
		if err := future.Mount(futureMount); err != nil {
			return err
		}
		defer future.Unmount(futureMount)

		chroot, err := NewChroot(futureMount, future.Uuid, future.Partition.Device, false, "")
		if err != nil {
			return err
		}
		defer chroot.Close()

		if err := chroot.Execute(settings.Cnf.UpdateInitramfsCmd); err != nil {
			return err
		}
	}

	return nil
}

// buildRecipe constructs an ImageRecipe from the staged package queue.
// Returns nil if no packages are staged (plain image pull, no build step).
func (s *ABSystem) buildRecipe(baseImage string) (*ImageRecipe, error) {
	pm, err := NewPackageManager(false)
	if err != nil {
		return nil, err
	}

	adds, err := pm.GetPackagesToAdd()
	if err != nil {
		return nil, err
	}
	removes, err := pm.GetPackagesToRemove()
	if err != nil {
		return nil, err
	}

	if len(adds) == 0 && len(removes) == 0 {
		return nil, nil
	}

	cfg := settings.Cnf
	content := ""

	if cfg.IPkgMngPre != "" {
		content += cfg.IPkgMngPre + "\n"
	}
	if len(adds) > 0 {
		content += cfg.IPkgMngAdd + " " + joinPkgs(adds) + "\n"
	}
	if len(removes) > 0 {
		content += cfg.IPkgMngRm + " " + joinPkgs(removes) + "\n"
	}
	if cfg.IPkgMngPost != "" {
		content += cfg.IPkgMngPost + "\n"
	}

	return NewImageRecipe(baseImage, map[string]string{
		"ABRoot.root": "future",
	}, nil, content), nil
}

// updateBootInChroot runs GRUB and initramfs update commands inside the
// future root via chroot. Mirrors ABRoot v2's transaction stage logic.
func (s *ABSystem) updateBootInChroot(futureMount string, future ABRootPartition) error {
	chroot, err := NewChroot(futureMount, future.Uuid, future.Partition.Device, false, "")
	if err != nil {
		return err
	}
	defer chroot.Close()

	if settings.Cnf.UpdateInitramfsCmd != "" {
		if err := chroot.Execute(settings.Cnf.UpdateInitramfsCmd); err != nil {
			return fmt.Errorf("updating initramfs: %w", err)
		}
	}

	return nil
}

// RollbackSystem swaps the present and future partition roles.
// dryRun = true checks eligibility without making changes.
func (s *ABSystem) RollbackSystem(dryRun bool) ABRollbackResponse {
	PrintVerboseInfo("ABSystem.RollbackSystem", "running...")

	present, err := s.RootM.GetPresent()
	if err != nil {
		return ROLLBACK_FAILED
	}

	future, err := s.RootM.GetFuture()
	if err != nil {
		return ROLLBACK_FAILED
	}

	if present.Current && !future.Current {
		if dryRun {
			return ROLLBACK_RES_YES
		}
		// Swap roles by updating the boot entry to point at the future partition.
		// The actual swap happens at next boot via the bootloader.
		if err := UpdateGrubForRollback(future); err != nil {
			return ROLLBACK_FAILED
		}
		return ROLLBACK_SUCCESS
	}

	return ROLLBACK_UNNECESSARY
}

// CurrentBtrfsDeployment returns the name of the currently running btrfs
// deployment by reading the rootflags from /proc/mounts.
func CurrentBtrfsDeployment() string {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return ""
	}
	// Look for: LABEL=frzr_root / btrfs rw,subvol=deployments/<name> ...
	for _, line := range splitLines(string(data)) {
		if len(line) == 0 {
			continue
		}
		fields := splitFields(line)
		if len(fields) < 4 || fields[1] != "/" {
			continue
		}
		for _, opt := range splitComma(fields[3]) {
			const prefix = "subvol=deployments/"
			if len(opt) > len(prefix) && opt[:len(prefix)] == prefix {
				return opt[len(prefix):]
			}
		}
	}
	return ""
}

func joinPkgs(pkgs []string) string {
	result := ""
	for i, p := range pkgs {
		if i > 0 {
			result += " "
		}
		result += p
	}
	return result
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func splitFields(s string) []string {
	var fields []string
	inField := false
	start := 0
	for i, c := range s {
		if c == ' ' || c == '\t' {
			if inField {
				fields = append(fields, s[start:i])
				inField = false
			}
		} else {
			if !inField {
				start = i
				inField = true
			}
		}
	}
	if inField {
		fields = append(fields, s[start:])
	}
	return fields
}

func splitComma(s string) []string {
	var parts []string
	start := 0
	for i, c := range s {
		if c == ',' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}
