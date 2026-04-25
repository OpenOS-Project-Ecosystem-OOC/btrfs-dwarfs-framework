module gitlab.com/openos-project/git-management_deving/gitlab-enhanced/tools/bdfs-pin-helper

go 1.21

require (
	gitlab.com/openos-project/git-management_deving/gitlab-enhanced/ipfs/dwarfs-pin v0.0.0
	modernc.org/sqlite v1.29.10
)

replace gitlab.com/openos-project/git-management_deving/gitlab-enhanced/ipfs/dwarfs-pin => ../../ipfs/dwarfs-pin
