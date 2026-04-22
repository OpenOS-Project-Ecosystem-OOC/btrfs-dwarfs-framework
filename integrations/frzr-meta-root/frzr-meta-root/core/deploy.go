package core

/*	License: GPLv3
	Authors:
		OpenOS Project Contributors
	Copyright: 2024
	Description:
		frzr-style btrfs subvolume deployment. This file brings the
		frzr deployment model into frzr-meta-root as an alternative to
		the A/B partition model. When deployMode = "btrfs" in config,
		the transaction engine calls these functions instead of the
		A/B rsync path.

		Image acquisition is still handled by IncusClient (core/incus.go),
		replacing frzr's GitHub Releases API + curl download.
*/

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// DefaultDeployPath is the btrfs subvolume directory, matching frzr convention.
	DefaultDeployPath = "/frzr_root/deployments"

	// BootCfgPath is the systemd-boot entry written by frzr-meta-root in btrfs mode.
	BootCfgPath = "/frzr_root/boot/loader/entries/frzr-meta-root.conf"
)

// DeploySubvolume receives a btrfs send-stream from r and creates a new
// read-only deployment subvolume at deployPath/name.
//
// This replaces the `btrfs receive` call in frzr's __frzr-deploy. The stream
// is produced by IncusClient.exportImageRootfs (via `incus image export`)
// rather than downloaded from GitHub Releases.
func DeploySubvolume(r io.Reader, deployPath, name string) error {
	PrintVerboseInfo("DeploySubvolume", "receiving subvolume", name)

	dest := filepath.Join(deployPath, name)
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("deployment %s already exists", name)
	}

	if err := os.MkdirAll(deployPath, 0o755); err != nil {
		return fmt.Errorf("creating deploy path: %w", err)
	}

	cmd := exec.Command("btrfs", "receive", "--quiet", deployPath)
	cmd.Stdin = r
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("btrfs receive: %w", err)
	}

	PrintVerboseInfo("DeploySubvolume", "received", name)
	return nil
}

// DeployFromRootfs copies a rootfs directory tree into a new btrfs subvolume
// at deployPath/name. Used when the image was exported as a tarball rather
// than a btrfs send-stream.
//
// This is the path taken when IncusClient.ExportRootFs produces a directory
// rather than a btrfs stream.
func DeployFromRootfs(rootfsDir, deployPath, name string) error {
	PrintVerboseInfo("DeployFromRootfs", rootfsDir, "→", name)

	dest := filepath.Join(deployPath, name)
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("deployment %s already exists", name)
	}

	// Create a new btrfs subvolume.
	cmd := exec.Command("btrfs", "subvolume", "create", dest)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("btrfs subvolume create %s: %w", dest, err)
	}

	// Populate it from the rootfs directory.
	if err := rsyncCmd(rootfsDir+"/", dest, []string{"--delete", "--delete-before", "--checksum"}, false); err != nil {
		// Clean up the empty subvolume on failure.
		_ = exec.Command("btrfs", "subvolume", "delete", dest).Run()
		return fmt.Errorf("populating subvolume: %w", err)
	}

	PrintVerboseInfo("DeployFromRootfs", "deployed", name)
	return nil
}

// PruneDeployments removes old subvolumes from deployPath, keeping current
// and nextBoot. Any other subvolume is deleted.
//
// This mirrors the cleanup logic in frzr's __frzr-deploy (get_deployment_to_delete).
func PruneDeployments(deployPath, current, nextBoot string) error {
	PrintVerboseInfo("PruneDeployments", "pruning", deployPath)

	entries, err := os.ReadDir(deployPath)
	if err != nil {
		return fmt.Errorf("reading deploy path: %w", err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == current || name == nextBoot {
			continue
		}

		subvol := filepath.Join(deployPath, name)
		PrintVerboseInfo("PruneDeployments", "deleting", name)

		// Archive the deployment as a DwarFS image before deletion so it
		// can be mounted read-only later via `bdfs blend mount`.
		// BdfsPostDeployHook is a no-op when bdfs is absent or disabled.
		bdfsResult := BdfsPostDeployHook(subvol, name)
		if bdfsResult.Error != "" {
			PrintVerboseErr("PruneDeployments", 0, "bdfs archive skipped or failed:", bdfsResult.Error)
		}

		cmd := exec.Command("btrfs", "subvolume", "delete", subvol)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			PrintVerboseErr("PruneDeployments", 0, "failed to delete", name, err)
			// Non-fatal: continue pruning other subvolumes.
		}
	}

	return nil
}

// WriteBootEntry writes a systemd-boot loader entry for the given deployment.
// This replaces get_boot_cfg in frzr's __frzr-deploy.
//
// The entry is written to BootCfgPath and references the kernel and initramfs
// copied from the deployment subvolume's /boot directory.
func WriteBootEntry(deployPath, name, additionalArgs string) error {
	PrintVerboseInfo("WriteBootEntry", "writing boot entry for", name)

	subvol := filepath.Join(deployPath, name)

	amdUcode := ""
	if _, err := os.Stat(filepath.Join(subvol, "boot", "amd-ucode.img")); err == nil {
		amdUcode = "initrd /" + name + "/amd-ucode.img\n"
	}

	intelUcode := ""
	if _, err := os.Stat(filepath.Join(subvol, "boot", "intel-ucode.img")); err == nil {
		intelUcode = "initrd /" + name + "/intel-ucode.img\n"
	}

	entry := fmt.Sprintf(
		"title %s\nlinux /%s/vmlinuz-linux\n%s%sinitrd /%s/initramfs-linux.img\noptions root=LABEL=frzr_root rw rootflags=subvol=deployments/%s quiet splash %s\n",
		name, name, amdUcode, intelUcode, name, name, additionalArgs,
	)

	if err := os.MkdirAll(filepath.Dir(BootCfgPath), 0o755); err != nil {
		return fmt.Errorf("creating boot entry dir: %w", err)
	}

	if err := os.WriteFile(BootCfgPath, []byte(entry), 0o644); err != nil {
		return fmt.Errorf("writing boot entry: %w", err)
	}

	loaderConf := filepath.Join(filepath.Dir(BootCfgPath), "..", "loader.conf")
	_ = os.WriteFile(loaderConf, []byte("default frzr-meta-root.conf\n"), 0o644)

	PrintVerboseInfo("WriteBootEntry", "wrote", BootCfgPath)
	return nil
}

// CopyKernelFiles copies vmlinuz, initramfs, and microcode images from the
// deployment subvolume's /boot into the EFI partition's per-deployment directory.
// This mirrors the cp commands in frzr's __frzr-deploy.
func CopyKernelFiles(deployPath, efiPath, name string) error {
	PrintVerboseInfo("CopyKernelFiles", "copying kernel files for", name)

	src := filepath.Join(deployPath, name, "boot")
	dst := filepath.Join(efiPath, name)

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("creating EFI dir: %w", err)
	}

	files := []string{"vmlinuz-linux", "initramfs-linux.img", "amd-ucode.img", "intel-ucode.img"}
	for _, f := range files {
		srcFile := filepath.Join(src, f)
		if _, err := os.Stat(srcFile); os.IsNotExist(err) {
			continue
		}
		if err := copyFile(srcFile, filepath.Join(dst, f)); err != nil {
			return fmt.Errorf("copying %s: %w", f, err)
		}
	}

	return nil
}

// ListDeployments returns the names of all subvolumes in deployPath, sorted
// by name (which encodes the version/timestamp).
func ListDeployments(deployPath string) ([]string, error) {
	entries, err := os.ReadDir(deployPath)
	if err != nil {
		return nil, fmt.Errorf("reading deploy path: %w", err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// NextBootDeployment reads the current boot entry and returns the deployment
// name it references, or "" if none is configured.
func NextBootDeployment(bootCfgPath string) string {
	data, err := os.ReadFile(bootCfgPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "title ") {
			return strings.TrimPrefix(line, "title ")
		}
	}
	return ""
}

// copyFile copies src to dst, preserving permissions.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
