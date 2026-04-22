// Package core — deploy_bdfs_hook.go
// BdfsPostDeployHook archives the previous frzr deployment as a DwarFS image
// before it is deleted, allowing it to be mounted read-only via bdfs later.
//
// Call site in deploy.go — add these two lines after DeploySubvolume() confirms
// the new deployment is active:
//
//	bdfsResult := BdfsPostDeployHook(previousDeploymentPath, previousDeploymentName)
//	if bdfsResult.Error != "" {
//	    log.Printf("bdfs archive skipped or failed: %s", bdfsResult.Error)
//	}
//
// The hook is a no-op when bdfs is not installed or disabled in /etc/bdfs/bdfs.conf.

package core

// BdfsPostDeployHook archives oldSubvolPath (the subvolume of the deployment
// being replaced) as a DwarFS image. deploymentName is used as the archive
// filename prefix (e.g. "steamdeck-20240315").
//
// Returns BdfsArchiveResult. Skipped=true means bdfs is absent or disabled —
// treat as non-fatal. Error != "" means archival was attempted but failed.
func BdfsPostDeployHook(oldSubvolPath, deploymentName string) BdfsArchiveResult {
	cfg := LoadBdfsConfig()
	return BdfsArchiveDeployment(cfg, oldSubvolPath, deploymentName)
}
