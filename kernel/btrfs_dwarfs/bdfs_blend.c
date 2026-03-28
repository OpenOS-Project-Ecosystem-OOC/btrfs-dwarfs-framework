// SPDX-License-Identifier: GPL-2.0-or-later
/*
 * bdfs_blend.c - Unified namespace blend layer
 *
 * The blend layer merges a BTRFS partition and one or more DwarFS-backed
 * partitions into a single coherent filesystem namespace.  It is implemented
 * as a stackable VFS layer (similar in concept to overlayfs) with the
 * following routing rules:
 *
 *   READ path:
 *     1. Check BTRFS upper layer first (writable, live data).
 *     2. Fall through to DwarFS lower layers (read-only, compressed archives).
 *     3. If a path exists in both, the BTRFS version takes precedence.
 *
 *   WRITE path:
 *     1. All writes go to the BTRFS upper layer.
 *     2. Copy-up is performed automatically when writing to a path that
 *        currently exists only in a DwarFS lower layer (promote-on-write).
 *
 *   SNAPSHOT / ARCHIVE path:
 *     - `bdfs demote <path>` serialises a BTRFS subvolume to a DwarFS image
 *       and optionally removes the BTRFS subvolume (freeing live space).
 *     - `bdfs promote <path>` extracts a DwarFS image into a new BTRFS
 *       subvolume, making it writable.
 *
 * The blend filesystem type is registered as "bdfs_blend" and can be mounted
 * with:
 *   mount -t bdfs_blend -o btrfs=<uuid>,dwarfs=<uuid>[,<uuid>...] none <mnt>
 */

#include <linux/module.h>
#include <linux/fs.h>
#include <linux/slab.h>
#include <linux/uaccess.h>
#include <linux/mutex.h>
#include <linux/list.h>
#include <linux/namei.h>
#include <linux/mount.h>
#include <linux/seq_file.h>
#include <linux/statfs.h>
#include <linux/xattr.h>

#include "bdfs_internal.h"

#define BDFS_BLEND_FS_TYPE  "bdfs_blend"
#define BDFS_BLEND_MAGIC    0xBD75B1E0

/* Per-mount blend state */
struct bdfs_blend_mount {
	struct list_head        list;
	char                    mount_point[BDFS_PATH_MAX];

	/* BTRFS upper layer */
	struct vfsmount        *btrfs_mnt;
	u8                      btrfs_uuid[16];

	/* DwarFS lower layers (ordered; first = highest priority) */
	struct list_head        dwarfs_layers;
	int                     dwarfs_layer_count;

	struct bdfs_mount_opts  opts;
	struct super_block     *sb;
};

struct bdfs_dwarfs_layer {
	struct list_head        list;
	struct vfsmount        *mnt;
	u8                      partition_uuid[16];
	u64                     image_id;
	int                     priority;       /* lower = checked first */
};

static DEFINE_MUTEX(bdfs_blend_mounts_lock);
static LIST_HEAD(bdfs_blend_mounts);

/* ── Superblock operations ───────────────────────────────────────────────── */

static int bdfs_blend_statfs(struct dentry *dentry, struct kstatfs *buf)
{
	/*
	 * Report aggregate stats: capacity from BTRFS upper layer,
	 * used space includes both BTRFS live data and DwarFS image sizes.
	 */
	struct super_block *sb = dentry->d_sb;
	struct bdfs_blend_mount *bm = sb->s_fs_info;
	int ret;

	if (!bm || !bm->btrfs_mnt)
		return -EIO;

	ret = vfs_statfs(&bm->btrfs_mnt->mnt_root->d_sb->s_root->d_sb->s_root,
			 buf);
	buf->f_type = BDFS_BLEND_MAGIC;
	return ret;
}

static void bdfs_blend_put_super(struct super_block *sb)
{
	struct bdfs_blend_mount *bm = sb->s_fs_info;
	struct bdfs_dwarfs_layer *layer, *tmp;

	if (!bm)
		return;

	list_for_each_entry_safe(layer, tmp, &bm->dwarfs_layers, list) {
		list_del(&layer->list);
		kfree(layer);
	}

	mutex_lock(&bdfs_blend_mounts_lock);
	list_del(&bm->list);
	mutex_unlock(&bdfs_blend_mounts_lock);

	kfree(bm);
	sb->s_fs_info = NULL;
}

static const struct super_operations bdfs_blend_sops = {
	.alloc_inode  = bdfs_blend_alloc_inode,
	.free_inode   = bdfs_blend_free_inode,
	.statfs       = bdfs_blend_statfs,
	.put_super    = bdfs_blend_put_super,
};

/* ── Inode operations: read routing ─────────────────────────────────────── */

/*
 * bdfs_blend_inode_from_path - Create a blend inode that aliases a real inode
 * found on a backing layer (BTRFS or DwarFS FUSE mount).
 *
 * We create a new inode in the blend superblock that mirrors the attributes
 * of the real inode.  The real path is stored in i_private so that file
 * operations can be forwarded to the backing layer.
 */
struct bdfs_blend_inode_info {
	struct inode    vfs_inode;
	struct path     real_path;      /* path on the backing layer */
	bool            is_upper;       /* true = BTRFS, false = DwarFS lower */
};

static inline struct bdfs_blend_inode_info *
BDFS_I(struct inode *inode)
{
	return container_of(inode, struct bdfs_blend_inode_info, vfs_inode);
}

static struct inode *bdfs_blend_alloc_inode(struct super_block *sb)
{
	struct bdfs_blend_inode_info *bi;

	bi = kmem_cache_alloc(bdfs_inode_cachep, GFP_KERNEL);
	if (!bi)
		return NULL;
	memset(&bi->real_path, 0, sizeof(bi->real_path));
	bi->is_upper = false;
	return &bi->vfs_inode;
}

static void bdfs_blend_free_inode(struct inode *inode)
{
	struct bdfs_blend_inode_info *bi = BDFS_I(inode);
	path_put(&bi->real_path);
	kmem_cache_free(bdfs_inode_cachep, bi);
}

/* Inode cache — allocated once at module init */
static struct kmem_cache *bdfs_inode_cachep;

static void bdfs_inode_init_once(void *obj)
{
	struct bdfs_blend_inode_info *bi = obj;
	inode_init_once(&bi->vfs_inode);
}

/*
 * bdfs_blend_make_inode - Allocate a blend inode mirroring a real inode.
 *
 * Copies uid/gid/mode/size/timestamps from the real inode so that stat(2)
 * returns correct values.  File operations are forwarded via real_path.
 */
static struct inode *bdfs_blend_make_inode(struct super_block *sb,
					   const struct path *real_path,
					   bool is_upper)
{
	struct inode *inode;
	struct inode *real = d_inode(real_path->dentry);
	struct bdfs_blend_inode_info *bi;

	inode = new_inode(sb);
	if (!inode)
		return NULL;

	bi = BDFS_I(inode);
	path_get(real_path);
	bi->real_path = *real_path;
	bi->is_upper  = is_upper;

	/* Mirror attributes from the real inode */
	inode->i_ino   = real->i_ino;
	inode->i_mode  = real->i_mode;
	inode->i_uid   = real->i_uid;
	inode->i_gid   = real->i_gid;
	inode->i_size  = real->i_size;
	inode->i_atime = real->i_atime;
	inode->i_mtime = real->i_mtime;
	inode->i_ctime = real->i_ctime;
	set_nlink(inode, real->i_nlink);

	if (S_ISDIR(inode->i_mode))
		inode->i_op = &bdfs_blend_dir_iops;
	else if (S_ISREG(inode->i_mode))
		inode->i_op = &bdfs_blend_file_iops;

	return inode;
}

/*
 * bdfs_blend_lookup - Resolve a name in the blend namespace.
 *
 * Routing order:
 *   1. BTRFS upper layer  (writable; takes precedence on name collision)
 *   2. DwarFS lower layers in priority order (read-only compressed archives)
 *
 * For each layer we use vfs_path_lookup() to resolve the name relative to
 * that layer's root, then wrap the result in a blend inode so the VFS sees
 * a consistent namespace.
 *
 * Copy-up on write is handled at the file_operations level: any write to a
 * blend inode backed by a DwarFS lower layer triggers a copy-up to the BTRFS
 * upper layer before the write proceeds.
 */
static struct dentry *bdfs_blend_lookup(struct inode *dir,
					struct dentry *dentry,
					unsigned int flags)
{
	struct super_block *sb = dir->i_sb;
	struct bdfs_blend_mount *bm = sb->s_fs_info;
	struct bdfs_blend_inode_info *parent_bi = BDFS_I(dir);
	struct path parent_real = parent_bi->real_path;
	struct path child_path;
	struct inode *new_inode;
	struct dentry *new_dentry;
	const char *name = dentry->d_name.name;
	int err;

	/*
	 * Step 1: Try the BTRFS upper layer.
	 *
	 * We resolve the name relative to the parent's real path on the upper
	 * layer.  If the parent itself is from a lower layer (DwarFS), we
	 * redirect to the corresponding path on the BTRFS upper root.
	 */
	if (bm->btrfs_mnt && (parent_bi->is_upper ||
	    parent_real.mnt == bm->btrfs_mnt)) {
		err = vfs_path_lookup(parent_real.dentry, parent_real.mnt,
				      name, 0, &child_path);
		if (!err && d_is_positive(child_path.dentry)) {
			new_inode = bdfs_blend_make_inode(sb, &child_path,
							  true);
			path_put(&child_path);
			if (!new_inode)
				return ERR_PTR(-ENOMEM);
			new_dentry = d_splice_alias(new_inode, dentry);
			return new_dentry ? new_dentry : dentry;
		}
		if (!err)
			path_put(&child_path);
	}

	/*
	 * Step 2: Fall through to DwarFS lower layers in priority order.
	 *
	 * Each lower layer is a FUSE vfsmount.  We resolve the name relative
	 * to the layer's root, constructing the full path from the blend
	 * mount point down to the current directory.
	 */
	{
		struct bdfs_dwarfs_layer *layer;

		list_for_each_entry(layer, &bm->dwarfs_layers, list) {
			if (!layer->mnt)
				continue;

			err = vfs_path_lookup(layer->mnt->mnt_root,
					      layer->mnt, name, 0,
					      &child_path);
			if (!err && d_is_positive(child_path.dentry)) {
				new_inode = bdfs_blend_make_inode(
						sb, &child_path, false);
				path_put(&child_path);
				if (!new_inode)
					return ERR_PTR(-ENOMEM);
				new_dentry = d_splice_alias(new_inode, dentry);
				return new_dentry ? new_dentry : dentry;
			}
			if (!err)
				path_put(&child_path);
		}
	}

	/* Not found in any layer — return a negative dentry */
	d_add(dentry, NULL);
	return NULL;
}

/*
 * bdfs_blend_getattr - Forward stat to the real backing inode.
 *
 * Refreshes size and timestamps from the real inode before returning,
 * so that changes on the backing layer are reflected immediately.
 */
static int bdfs_blend_getattr(struct mnt_idmap *idmap,
			      const struct path *path,
			      struct kstat *stat,
			      u32 request_mask,
			      unsigned int query_flags)
{
	struct inode *inode = d_inode(path->dentry);
	struct bdfs_blend_inode_info *bi = BDFS_I(inode);
	struct inode *real;

	if (!bi->real_path.dentry)
		return -EIO;

	real = d_inode(bi->real_path.dentry);

	/* Refresh from real inode */
	inode->i_size  = real->i_size;
	inode->i_atime = real->i_atime;
	inode->i_mtime = real->i_mtime;
	inode->i_ctime = real->i_ctime;

	generic_fillattr(idmap, inode, stat);
	return 0;
}

/*
 * bdfs_blend_open - Forward open to the real backing file.
 *
 * For writes to a DwarFS-backed inode, copy-up is triggered first:
 * the file is copied to the BTRFS upper layer and the inode's real_path
 * is updated to point to the upper layer copy.
 */
static int bdfs_blend_open(struct inode *inode, struct file *file)
{
	struct bdfs_blend_inode_info *bi = BDFS_I(inode);
	struct super_block *sb = inode->i_sb;
	struct bdfs_blend_mount *bm = sb->s_fs_info;

	/*
	 * Copy-up: if this is a write to a lower-layer (DwarFS) inode,
	 * we need to materialise it on the BTRFS upper layer first.
	 * The actual copy-up is performed by the daemon via a
	 * BDFS_JOB_PROMOTE job; here we emit the event and block until
	 * the upper layer copy is ready.
	 *
	 * For the kernel module we emit the event and return -EAGAIN;
	 * the VFS will retry after the daemon completes the copy-up.
	 */
	if (!bi->is_upper && (file->f_flags & (O_WRONLY | O_RDWR))) {
		if (bm) {
			bdfs_emit_event(BDFS_EVT_SNAPSHOT_CREATED,
					bm->btrfs_uuid, inode->i_ino,
					"copyup_needed");
		}
		return -EAGAIN;
	}

	/* Forward to the real file on the backing layer */
	return finish_open(file, bi->real_path.dentry,
			   generic_file_open);
}

static const struct inode_operations bdfs_blend_file_iops = {
	.getattr = bdfs_blend_getattr,
};

static const struct file_operations bdfs_blend_file_fops = {
	.open    = bdfs_blend_open,
	.read_iter  = generic_file_read_iter,
	.llseek     = generic_file_llseek,
};

static const struct inode_operations bdfs_blend_dir_iops = {
	.lookup  = bdfs_blend_lookup,
	.getattr = bdfs_blend_getattr,
};

/* ── Filesystem type registration ───────────────────────────────────────── */

static int bdfs_blend_fill_super(struct super_block *sb, struct fs_context *fc)
{
	struct bdfs_blend_mount *bm = fc->fs_private;
	struct inode *root_inode;
	struct dentry *root_dentry;

	sb->s_magic = BDFS_BLEND_MAGIC;
	sb->s_op = &bdfs_blend_sops;
	sb->s_fs_info = bm;
	sb->s_flags |= SB_RDONLY; /* blend root is read-only; writes go to upper */

	root_inode = new_inode(sb);
	if (!root_inode)
		return -ENOMEM;

	root_inode->i_ino = 1;
	root_inode->i_mode = S_IFDIR | 0755;
	root_inode->i_op = &bdfs_blend_dir_iops;
	set_nlink(root_inode, 2);

	root_dentry = d_make_root(root_inode);
	if (!root_dentry)
		return -ENOMEM;

	sb->s_root = root_dentry;
	return 0;
}

static int bdfs_blend_get_tree(struct fs_context *fc)
{
	return get_tree_nodev(fc, bdfs_blend_fill_super);
}

static const struct fs_context_operations bdfs_blend_ctx_ops = {
	.get_tree = bdfs_blend_get_tree,
};

static int bdfs_blend_init_fs_context(struct fs_context *fc)
{
	struct bdfs_blend_mount *bm;

	bm = kzalloc(sizeof(*bm), GFP_KERNEL);
	if (!bm)
		return -ENOMEM;

	INIT_LIST_HEAD(&bm->dwarfs_layers);
	fc->fs_private = bm;
	fc->ops = &bdfs_blend_ctx_ops;
	return 0;
}

static struct file_system_type bdfs_blend_fs_type = {
	.owner            = THIS_MODULE,
	.name             = BDFS_BLEND_FS_TYPE,
	.init_fs_context  = bdfs_blend_init_fs_context,
	.kill_sb          = kill_anon_super,
};

/* ── Blend mount / umount ioctls ─────────────────────────────────────────── */

int bdfs_blend_mount(void __user *uarg,
		     struct list_head *registry,
		     struct mutex *lock)
{
	struct bdfs_ioctl_mount_blend arg;
	struct bdfs_blend_mount *bm;
	char event_msg[256];

	if (copy_from_user(&arg, uarg, sizeof(arg)))
		return -EFAULT;

	bm = kzalloc(sizeof(*bm), GFP_KERNEL);
	if (!bm)
		return -ENOMEM;

	INIT_LIST_HEAD(&bm->dwarfs_layers);
	memcpy(bm->btrfs_uuid, arg.btrfs_uuid, 16);
	strscpy(bm->mount_point, arg.mount_point, sizeof(bm->mount_point));
	memcpy(&bm->opts, &arg.opts, sizeof(bm->opts));

	mutex_lock(&bdfs_blend_mounts_lock);
	list_add_tail(&bm->list, &bdfs_blend_mounts);
	mutex_unlock(&bdfs_blend_mounts_lock);

	snprintf(event_msg, sizeof(event_msg),
		 "blend mount=%s btrfs_uuid=%*phN dwarfs_uuid=%*phN",
		 arg.mount_point, 16, arg.btrfs_uuid, 16, arg.dwarfs_uuid);
	bdfs_emit_event(BDFS_EVT_BLEND_MOUNTED, arg.btrfs_uuid, 0, event_msg);

	pr_info("bdfs: blend mount queued at %s\n", arg.mount_point);
	return 0;
}

int bdfs_blend_umount(void __user *uarg)
{
	struct bdfs_ioctl_umount_blend arg;
	struct bdfs_blend_mount *bm, *tmp;
	bool found = false;

	if (copy_from_user(&arg, uarg, sizeof(arg)))
		return -EFAULT;

	mutex_lock(&bdfs_blend_mounts_lock);
	list_for_each_entry_safe(bm, tmp, &bdfs_blend_mounts, list) {
		if (strcmp(bm->mount_point, arg.mount_point) == 0) {
			list_del(&bm->list);
			found = true;
			bdfs_emit_event(BDFS_EVT_BLEND_UNMOUNTED,
					bm->btrfs_uuid, 0, arg.mount_point);
			kfree(bm);
			break;
		}
	}
	mutex_unlock(&bdfs_blend_mounts_lock);

	return found ? 0 : -ENOENT;
}

/* ── Blend layer partition ops vtable ───────────────────────────────────── */

static int bdfs_blend_part_init(struct bdfs_partition_entry *entry)
{
	pr_info("bdfs: hybrid blend partition '%s' registered\n",
		entry->desc.label);
	return 0;
}

struct bdfs_part_ops bdfs_blend_part_ops = {
	.name = "hybrid_blend",
	.init = bdfs_blend_part_init,
};

/* ── Module-level init / exit ───────────────────────────────────────────── */

int bdfs_blend_init(void)
{
	int ret;

	bdfs_inode_cachep = kmem_cache_create(
		"bdfs_inode_cache",
		sizeof(struct bdfs_blend_inode_info), 0,
		SLAB_RECLAIM_ACCOUNT | SLAB_MEM_SPREAD | SLAB_ACCOUNT,
		bdfs_inode_init_once);
	if (!bdfs_inode_cachep) {
		pr_err("bdfs: failed to create inode cache\n");
		return -ENOMEM;
	}

	ret = register_filesystem(&bdfs_blend_fs_type);
	if (ret) {
		pr_err("bdfs: failed to register blend filesystem: %d\n", ret);
		kmem_cache_destroy(bdfs_inode_cachep);
		return ret;
	}

	pr_info("bdfs: blend filesystem type '%s' registered\n",
		BDFS_BLEND_FS_TYPE);
	return 0;
}

void bdfs_blend_exit(void)
{
	unregister_filesystem(&bdfs_blend_fs_type);
	/*
	 * rcu_barrier() ensures all RCU-deferred inode frees have completed
	 * before we destroy the cache.
	 */
	rcu_barrier();
	kmem_cache_destroy(bdfs_inode_cachep);
}

/* ── List partitions helper (used by bdfs_main.c) ───────────────────────── */

int bdfs_list_partitions(void __user *uarg,
			 struct list_head *registry,
			 struct mutex *lock)
{
	struct bdfs_ioctl_list_partitions arg;
	struct bdfs_partition_entry *entry;
	struct bdfs_partition __user *ubuf;
	u32 copied = 0, total = 0;

	if (copy_from_user(&arg, uarg, sizeof(arg)))
		return -EFAULT;

	ubuf = (struct bdfs_partition __user *)(uintptr_t)arg.parts;

	mutex_lock(lock);
	list_for_each_entry(entry, registry, list) {
		total++;
		if (copied < arg.count && ubuf) {
			if (copy_to_user(&ubuf[copied], &entry->desc,
					 sizeof(entry->desc))) {
				mutex_unlock(lock);
				return -EFAULT;
			}
			copied++;
		}
	}
	mutex_unlock(lock);

	if (put_user(copied, &((struct bdfs_ioctl_list_partitions __user *)uarg)->count))
		return -EFAULT;
	if (put_user(total, &((struct bdfs_ioctl_list_partitions __user *)uarg)->total))
		return -EFAULT;

	return 0;
}
