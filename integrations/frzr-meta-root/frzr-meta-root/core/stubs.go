package core

// stubs.go contains placeholder implementations for ABRoot v2 core files
// not yet ported. Each stub will be replaced when the corresponding file
// is copied from ABRoot v2 (only a module path rename is required — none
// of these files have OCI dependencies).
//
// Files to port:
//   core/root.go          -> ABRootManager, ABRootPartition
//   core/disk-manager.go  -> DiskManager, Partition
//   core/chroot.go        -> Chroot, NewChroot
//   core/grub.go          -> UpdateGrubForRollback
//   core/kargs.go         -> KargsEdit, KargsShow
//   core/kernel.go        -> kernel copy helpers
//   core/packages.go      -> PackageManager, NewPackageManager
//   core/package-diff.go  -> package diff helpers
//   core/image.go         -> ABImage, NewABImage, NewABImageFromRoot
//   core/image-recipe.go  -> ImageRecipe, NewImageRecipe
//   core/checks.go        -> Checks, NewChecks
//   core/integrity.go     -> integrity helpers
//   core/atomic-io.go     -> atomic file write helpers
//   core/rsync.go         -> rsyncCmd
//   core/logging.go       -> PrintVerboseInfo, PrintVerboseErr
//   core/diff.go          -> Differ API integration

import (
	"fmt"
	"os"
	"time"

	digest "github.com/opencontainers/go-digest"
)

// ---------------------------------------------------------------------------
// Logging (core/logging.go)
// ---------------------------------------------------------------------------

func PrintVerboseInfo(caller string, args ...interface{}) {}
func PrintVerboseErr(caller string, args ...interface{})  {}

// ---------------------------------------------------------------------------
// ABImage (core/image.go)
// ---------------------------------------------------------------------------

type ABImage struct {
	Digest    digest.Digest
	Timestamp time.Time
	Image     string
}

func NewABImage(d digest.Digest, image string) (*ABImage, error) {
	if d == "" {
		return nil, fmt.Errorf("digest is empty")
	}
	return &ABImage{Digest: d, Image: image, Timestamp: time.Now()}, nil
}

func NewABImageFromRoot() (*ABImage, error) {
	return nil, fmt.Errorf("stub: not yet ported from ABRoot v2 core/image.go")
}

func (a *ABImage) WriteTo(dest string) error {
	return fmt.Errorf("stub: not yet ported from ABRoot v2 core/image.go")
}

// ---------------------------------------------------------------------------
// ImageRecipe (core/image-recipe.go)
// ---------------------------------------------------------------------------

type ImageRecipe struct {
	From    string
	Labels  map[string]string
	Args    map[string]string
	Content string
}

func NewImageRecipe(image string, labels, args map[string]string, content string) *ImageRecipe {
	return &ImageRecipe{From: image, Labels: labels, Args: args, Content: content}
}

// ---------------------------------------------------------------------------
// Checks (core/checks.go)
// ---------------------------------------------------------------------------

type Checks struct{}

func NewChecks() *Checks { return &Checks{} }

func (c *Checks) PerformAllChecks() error {
	return fmt.Errorf("stub: not yet ported from ABRoot v2 core/checks.go")
}

// ---------------------------------------------------------------------------
// Partition / DiskManager (core/disk-manager.go)
// ---------------------------------------------------------------------------

type Partition struct {
	Label        string
	MountPoint   string
	MountOptions string
	Uuid         string
	FsType       string
	Device       string
}

// ---------------------------------------------------------------------------
// ABRootManager / ABRootPartition (core/root.go)
// ---------------------------------------------------------------------------

type ABRootPartition struct {
	Label        string
	IdentifiedAs string
	Partition    Partition
	MountPoint   string
	MountOptions string
	Uuid         string
	FsType       string
	Current      bool
}

func (p ABRootPartition) Mount(dest string) error {
	return fmt.Errorf("stub: not yet ported from ABRoot v2 core/root.go")
}

func (p ABRootPartition) Unmount(dest string) error {
	return fmt.Errorf("stub: not yet ported from ABRoot v2 core/root.go")
}

type ABRootManager struct {
	Partitions   []ABRootPartition
	VarPartition Partition
}

func NewABRootManager() *ABRootManager { return &ABRootManager{} }

func (a *ABRootManager) GetFuture() (ABRootPartition, error) {
	return ABRootPartition{}, fmt.Errorf("stub: not yet ported from ABRoot v2 core/root.go")
}

func (a *ABRootManager) GetPresent() (ABRootPartition, error) {
	return ABRootPartition{}, fmt.Errorf("stub: not yet ported from ABRoot v2 core/root.go")
}

// ---------------------------------------------------------------------------
// Chroot (core/chroot.go)
// ---------------------------------------------------------------------------

type Chroot struct{}

func NewChroot(root, rootUuid, rootDevice string, mountUserEtc bool, userEtcPath string) (*Chroot, error) {
	return nil, fmt.Errorf("stub: not yet ported from ABRoot v2 core/chroot.go")
}

func (c *Chroot) Execute(cmd string) error {
	return fmt.Errorf("stub: not yet ported from ABRoot v2 core/chroot.go")
}

func (c *Chroot) Close() error { return nil }

// ---------------------------------------------------------------------------
// GRUB (core/grub.go)
// ---------------------------------------------------------------------------

func UpdateGrubForRollback(future ABRootPartition) error {
	return fmt.Errorf("stub: not yet ported from ABRoot v2 core/grub.go")
}

// ---------------------------------------------------------------------------
// Kernel args (core/kargs.go)
// ---------------------------------------------------------------------------

func KargsEdit() error {
	return fmt.Errorf("stub: not yet ported from ABRoot v2 core/kargs.go")
}

func KargsShow() (string, error) {
	return "", fmt.Errorf("stub: not yet ported from ABRoot v2 core/kargs.go")
}

// ---------------------------------------------------------------------------
// PackageManager (core/packages.go)
// ---------------------------------------------------------------------------

const (
	ADD    = "+"
	REMOVE = "-"
)

type PackageManager struct{}

func NewPackageManager(dryRun bool) (*PackageManager, error) {
	return &PackageManager{}, nil
}

func (pm *PackageManager) Add(pkg, op string) error {
	return fmt.Errorf("stub: not yet ported from ABRoot v2 core/packages.go")
}

func (pm *PackageManager) GetPackagesToAdd() ([]string, error)    { return nil, nil }
func (pm *PackageManager) GetPackagesToRemove() ([]string, error) { return nil, nil }

// ---------------------------------------------------------------------------
// rsync (core/rsync.go)
// ---------------------------------------------------------------------------

func rsyncCmd(src, dst string, args []string, dryRun bool) error {
	return fmt.Errorf("stub: not yet ported from ABRoot v2 core/rsync.go")
}

// ---------------------------------------------------------------------------
// getDirSize / walkDir helpers
// ---------------------------------------------------------------------------

func getDirSize(path string) (int64, error) {
	var size int64
	err := walkDir(path, func(p string, info os.FileInfo) error {
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

func walkDir(path string, fn func(string, os.FileInfo) error) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			return err
		}
		full := path + "/" + e.Name()
		if err := fn(full, info); err != nil {
			return err
		}
		if e.IsDir() {
			if err := walkDir(full, fn); err != nil {
				return err
			}
		}
	}
	return nil
}
