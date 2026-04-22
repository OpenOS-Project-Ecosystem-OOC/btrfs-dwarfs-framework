package core

/*	License: GPLv3
	Authors:
		OpenOS Project Contributors
	Copyright: 2024
	Description:
		Incus-backed OCI image operations. Replaces the Podman/Buildah/prometheus
		stack from upstream ABRoot v2. All image pull, build, export, and
		update-check operations go through the Incus Go client API.
*/

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/ioprogress"
	digest "github.com/opencontainers/go-digest"
)

// OCIBackend is the interface the ABSystem transaction engine uses for all
// image operations. Implementing this interface is the only change required
// to swap backends.
type OCIBackend interface {
	PullImage(name, tag string) (string, error)
	ExportRootFs(name, tag string, recipe *ImageRecipe, transDir, dest string) error
	BuildImage(baseFingerprint string, recipe *ImageRecipe, alias string) (string, error)
	HasUpdate(name, tag string, currentDigest digest.Digest) (digest.Digest, bool, error)
	FindImageWithLabel(key, value string) (string, error)
	DeleteAllButLatestImage() error
}

// IncusClient wraps the Incus InstanceServer for image operations used by
// the transaction engine.
type IncusClient struct {
	server      incus.InstanceServer
	remote      string
	storagePool string
}

// discardSeeker is an io.WriteSeeker that discards all data.
// Used for the MetaFile field of ImageFileRequest when we only want the rootfs.
type discardSeeker struct{}

func (discardSeeker) Write(p []byte) (int, error) { return len(p), nil }
func (discardSeeker) Seek(offset int64, whence int) (int64, error) { return 0, nil }

// NewIncusClient connects to the Incus daemon at socketPath.
func NewIncusClient(socketPath, remote, pool string) (*IncusClient, error) {
	PrintVerboseInfo("NewIncusClient", "connecting to", socketPath)

	srv, err := incus.ConnectIncusUnix(socketPath, nil)
	if err != nil {
		PrintVerboseErr("NewIncusClient", 0, err)
		return nil, fmt.Errorf("connecting to Incus at %s: %w", socketPath, err)
	}

	return &IncusClient{server: srv, remote: remote, storagePool: pool}, nil
}

// PullImage pulls an OCI image from the configured registry remote into local
// Incus image storage and returns the local image fingerprint.
//
// Replaces OciPullImage + pullImageWithProgressbar from ABRoot v2 oci.go.
func (c *IncusClient) PullImage(name, tag string) (string, error) {
	PrintVerboseInfo("IncusClient.PullImage", "pulling", name+":"+tag)

	alias := name + ":" + tag

	op, err := c.server.CreateImage(api.ImagesPost{
		Aliases: []api.ImageAlias{{Name: alias}},
		Source: &api.ImagesPostSource{
			ImageSource: api.ImageSource{Server: c.remote, Alias: alias},
			Type:        "image",
		},
	}, nil)
	if err != nil {
		return "", fmt.Errorf("pulling image %s:%s: %w", name, tag, err)
	}
	if err := op.Wait(); err != nil {
		return "", fmt.Errorf("waiting for image pull %s:%s: %w", name, tag, err)
	}

	img, _, err := c.server.GetImageAlias(alias)
	if err != nil {
		return "", fmt.Errorf("resolving alias after pull: %w", err)
	}

	PrintVerboseInfo("IncusClient.PullImage", "pulled fingerprint", img.Target[:12])
	return img.Target, nil
}

// ExportRootFs pulls an OCI image, optionally applies a recipe via BuildImage,
// and exports the resulting rootfs to dest.
//
// Replaces OciExportRootFs from ABRoot v2 oci.go.
func (c *IncusClient) ExportRootFs(name, tag string, recipe *ImageRecipe, transDir, dest string) error {
	PrintVerboseInfo("IncusClient.ExportRootFs", "running...")

	if transDir == dest {
		return fmt.Errorf("ExportRootFs: transDir and dest cannot be the same")
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("creating dest dir: %w", err)
	}

	fingerprint, err := c.PullImage(name, tag)
	if err != nil {
		return err
	}

	finalFingerprint := fingerprint
	if recipe != nil && strings.TrimSpace(recipe.Content) != "" {
		built, err := c.BuildImage(fingerprint, recipe, "fmr-build-"+tag+"-"+time.Now().Format("20060102150405"))
		if err != nil {
			return err
		}
		finalFingerprint = built
		defer func() {
			op, err := c.server.DeleteImage(finalFingerprint)
			if err == nil {
				_ = op.Wait()
			}
		}()
	}

	if err := os.MkdirAll(transDir, 0o755); err != nil {
		return fmt.Errorf("creating transDir: %w", err)
	}
	tarPath := filepath.Join(transDir, "rootfs.tar.gz")
	if err := c.exportImageRootfs(finalFingerprint, tarPath); err != nil {
		return err
	}
	defer os.Remove(tarPath)

	if err := rsyncCmd(tarPath, dest, []string{"--archive", "--delete"}, false); err != nil {
		return fmt.Errorf("extracting rootfs: %w", err)
	}

	PrintVerboseInfo("IncusClient.ExportRootFs", "done, rootfs at", dest)
	return nil
}

// BuildImage launches a temporary container from baseFingerprint, applies
// recipe.Content line-by-line via incus exec, then publishes the result.
//
// Replaces buildah.BuildContainerFile from ABRoot v2 oci.go.
func (c *IncusClient) BuildImage(baseFingerprint string, recipe *ImageRecipe, alias string) (string, error) {
	PrintVerboseInfo("IncusClient.BuildImage", "building on top of", baseFingerprint[:12])

	containerName := "fmr-build-" + baseFingerprint[:8]

	op, err := c.server.CreateInstance(api.InstancesPost{
		Name: containerName,
		Source: api.InstanceSource{
			Type:        "image",
			Fingerprint: baseFingerprint,
		},
		InstancePut: api.InstancePut{Profiles: []string{"default"}},
	})
	if err != nil {
		return "", fmt.Errorf("creating build container: %w", err)
	}
	if err := op.Wait(); err != nil {
		return "", fmt.Errorf("waiting for build container creation: %w", err)
	}

	defer func() {
		stopOp, _ := c.server.UpdateInstanceState(containerName, api.InstanceStatePut{Action: "stop", Force: true}, "")
		if stopOp != nil {
			_ = stopOp.Wait()
		}
		delOp, _ := c.server.DeleteInstance(containerName)
		if delOp != nil {
			_ = delOp.Wait()
		}
	}()

	startOp, err := c.server.UpdateInstanceState(containerName, api.InstanceStatePut{Action: "start"}, "")
	if err != nil {
		return "", fmt.Errorf("starting build container: %w", err)
	}
	if err := startOp.Wait(); err != nil {
		return "", fmt.Errorf("waiting for build container start: %w", err)
	}

	for _, line := range strings.Split(recipe.Content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		execOp, err := c.server.ExecInstance(containerName, api.InstanceExecPost{
			Command:     []string{"/bin/sh", "-c", line},
			WaitForWS:   true,
			Interactive: false,
		}, &incus.InstanceExecArgs{
			Stdout: os.Stdout,
			Stderr: os.Stderr,
			Stdin:  io.NopCloser(strings.NewReader("")),
		})
		if err != nil {
			return "", fmt.Errorf("exec %q: %w", line, err)
		}
		if err := execOp.Wait(); err != nil {
			return "", fmt.Errorf("waiting for exec %q: %w", line, err)
		}
	}

	stopOp, err := c.server.UpdateInstanceState(containerName, api.InstanceStatePut{Action: "stop"}, "")
	if err != nil {
		return "", fmt.Errorf("stopping build container: %w", err)
	}
	if err := stopOp.Wait(); err != nil {
		return "", fmt.Errorf("waiting for build container stop: %w", err)
	}

	pubOp, err := c.server.CreateImage(api.ImagesPost{
		Source:  &api.ImagesPostSource{Type: "container", Name: containerName},
		Aliases: []api.ImageAlias{{Name: alias}},
	}, nil)
	if err != nil {
		return "", fmt.Errorf("publishing build container: %w", err)
	}
	if err := pubOp.Wait(); err != nil {
		return "", fmt.Errorf("waiting for image publish: %w", err)
	}

	img, _, err := c.server.GetImageAlias(alias)
	if err != nil {
		return "", fmt.Errorf("resolving published image alias: %w", err)
	}

	PrintVerboseInfo("IncusClient.BuildImage", "built fingerprint", img.Target[:12])
	return img.Target, nil
}

// HasUpdate checks whether the registry has a newer digest for name:tag than
// currentDigest. Pulls the image metadata to compare fingerprints.
//
// Replaces HasUpdate from ABRoot v2 oci.go (prometheus.PullManifestOnly).
func (c *IncusClient) HasUpdate(name, tag string, currentDigest digest.Digest) (digest.Digest, bool, error) {
	PrintVerboseInfo("IncusClient.HasUpdate", "checking", name+":"+tag)

	probeAlias := "fmr-probe-" + strings.ReplaceAll(name, "/", "-") + "-" + tag

	op, err := c.server.CreateImage(api.ImagesPost{
		Aliases: []api.ImageAlias{{Name: probeAlias}},
		Source: &api.ImagesPostSource{
			ImageSource: api.ImageSource{Server: c.remote, Alias: name + ":" + tag},
			Type:        "image",
		},
	}, nil)
	if err != nil {
		return "", false, fmt.Errorf("probing image %s:%s: %w", name, tag, err)
	}
	if err := op.Wait(); err != nil {
		return "", false, fmt.Errorf("waiting for image probe: %w", err)
	}

	img, _, err := c.server.GetImageAlias(probeAlias)
	if err != nil {
		return "", false, fmt.Errorf("resolving probe alias: %w", err)
	}

	defer func() {
		delOp, err := c.server.DeleteImage(img.Target)
		if err == nil {
			_ = delOp.Wait()
		}
	}()

	newDigest := digest.Digest("sha256:" + img.Target)
	if newDigest == currentDigest {
		PrintVerboseInfo("IncusClient.HasUpdate", "no update available")
		return "", false, nil
	}

	PrintVerboseInfo("IncusClient.HasUpdate", "update available:", img.Target[:12])
	return newDigest, true, nil
}

// FindImageWithLabel returns the fingerprint of the first locally cached image
// with the given key=value property.
//
// Replaces FindImageWithLabel from ABRoot v2 oci.go.
func (c *IncusClient) FindImageWithLabel(key, value string) (string, error) {
	images, err := c.server.GetImages()
	if err != nil {
		return "", fmt.Errorf("listing images: %w", err)
	}
	for _, img := range images {
		if v, ok := img.Properties[key]; ok && v == value {
			return img.Fingerprint, nil
		}
	}
	return "", nil
}

// DeleteAllButLatestImage removes all locally cached images except the newest.
//
// Replaces DeleteAllButLatestImage from ABRoot v2 oci.go.
func (c *IncusClient) DeleteAllButLatestImage() error {
	images, err := c.server.GetImages()
	if err != nil {
		return fmt.Errorf("listing images: %w", err)
	}
	if len(images) == 0 {
		return nil
	}

	var latest *api.Image
	for i := range images {
		if latest == nil || images[i].CreatedAt.After(latest.CreatedAt) {
			latest = &images[i]
		}
	}

	for _, img := range images {
		if img.Fingerprint == latest.Fingerprint {
			continue
		}
		op, err := c.server.DeleteImage(img.Fingerprint)
		if err != nil {
			PrintVerboseErr("IncusClient.DeleteAllButLatestImage", 0, err)
			continue
		}
		_ = op.Wait()
	}
	return nil
}

// exportImageRootfs exports the rootfs of a local Incus image to a tar.gz.
func (c *IncusClient) exportImageRootfs(fingerprint, destPath string) error {
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("creating export file: %w", err)
	}
	defer f.Close()

	_, err = c.server.GetImageFile(fingerprint, incus.ImageFileRequest{
		RootfsFile: f,
		MetaFile:   discardSeeker{},
		ProgressHandler: func(p ioprogress.ProgressData) {
			PrintVerboseInfo("IncusClient.exportImageRootfs", "progress:", p.Text)
		},
	})
	if err != nil {
		return fmt.Errorf("exporting image %s: %w", fingerprint[:12], err)
	}
	return nil
}

// Ensure IncusClient satisfies OCIBackend at compile time.
var _ OCIBackend = (*IncusClient)(nil)
