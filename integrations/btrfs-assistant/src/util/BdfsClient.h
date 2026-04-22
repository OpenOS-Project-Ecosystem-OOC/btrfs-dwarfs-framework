#ifndef BDFSCLIENT_H
#define BDFSCLIENT_H

#include <QJsonArray>
#include <QJsonObject>
#include <QLocalSocket>
#include <QObject>
#include <QString>
#include <QStringList>

/**
 * @brief Unix socket client for the btrfs-dwarfs-framework daemon.
 *
 * Sends JSON commands to the bdfs daemon socket (default:
 * /run/bdfs/bdfs.sock) and returns parsed responses.  All operations are
 * synchronous with a configurable timeout.
 *
 * All methods return an empty/default value and log a warning when the
 * daemon is unreachable, so callers can treat a missing daemon as a
 * graceful degradation rather than a hard error.
 */
class BdfsClient : public QObject {
    Q_OBJECT

  public:
    explicit BdfsClient(const QString &socketPath = QStringLiteral("/run/bdfs/bdfs.sock"),
                        QObject *parent = nullptr);

    /** @brief Returns true if the daemon socket exists and accepts connections. */
    bool isAvailable() const;

    // -----------------------------------------------------------------------
    // Partition / image / mount queries
    // -----------------------------------------------------------------------

    /** @brief Returns a list of BTRFS partition paths known to the daemon. */
    QStringList listPartitions() const;

    /** @brief Returns a list of DwarFS image paths managed by the daemon. */
    QStringList listImages() const;

    /** @brief Returns a list of currently active blend-mount paths. */
    QStringList listMounts() const;

    /** @brief Returns a JSON object with daemon status fields. */
    QJsonObject status() const;

    // -----------------------------------------------------------------------
    // Mount / unmount operations
    // -----------------------------------------------------------------------

    /**
     * @brief Mount a DwarFS image blended over a BTRFS subvolume.
     * @param imagePath   Path to the .dwarfs image.
     * @param subvolPath  Path to the BTRFS subvolume to blend over.
     * @param mountPoint  Target mount point.
     * @param userspace   Use fuse-overlayfs fallback instead of kernel module.
     * @return true on success.
     */
    bool mountBlend(const QString &imagePath, const QString &subvolPath,
                    const QString &mountPoint, bool userspace = false);

    /**
     * @brief Unmount a blend mount point.
     * @param mountPoint  The mount point to unmount.
     * @return true on success.
     */
    bool umount(const QString &mountPoint);

    // -----------------------------------------------------------------------
    // Snapshot operations
    // -----------------------------------------------------------------------

    /**
     * @brief Demote a BTRFS snapshot to a DwarFS image.
     * @param subvolPath  Path to the read-only BTRFS subvolume.
     * @param outputPath  Destination .dwarfs file path.
     * @return true on success.
     */
    bool demote(const QString &subvolPath, const QString &outputPath);

    /**
     * @brief Import a DwarFS image as a BTRFS subvolume.
     * @param imagePath   Path to the .dwarfs file.
     * @param targetPath  Destination BTRFS subvolume path.
     * @return true on success.
     */
    bool importImage(const QString &imagePath, const QString &targetPath);

    /**
     * @brief Prune snapshots matching a pattern, keeping the N most recent.
     * @param subvolPath   BTRFS subvolume whose snapshots to prune.
     * @param keep         Number of snapshots to retain.
     * @param pattern      Glob pattern to filter snapshot names (empty = all).
     * @param demoteFirst  Demote to DwarFS before deleting.
     * @param dryRun       Report what would be deleted without acting.
     * @return true on success.
     */
    bool prune(const QString &subvolPath, int keep, const QString &pattern = {},
               bool demoteFirst = false, bool dryRun = false);

  private:
    QString m_socketPath;
    int m_timeoutMs = 5000;

    /** @brief Send a JSON command and return the parsed response object. */
    QJsonObject sendCommand(const QJsonObject &cmd) const;

    /** @brief Returns true if the response indicates success. */
    static bool isSuccess(const QJsonObject &response);
};

#endif // BDFSCLIENT_H
