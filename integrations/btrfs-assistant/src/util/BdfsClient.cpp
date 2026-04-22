#include "BdfsClient.h"

#include <QJsonDocument>
#include <QLocalSocket>
#include <QLoggingCategory>

Q_LOGGING_CATEGORY(lcBdfs, "btrfs-assistant.bdfs")

BdfsClient::BdfsClient(const QString &socketPath, QObject *parent)
    : QObject(parent), m_socketPath(socketPath)
{
}

bool BdfsClient::isAvailable() const
{
    QLocalSocket sock;
    sock.connectToServer(m_socketPath);
    const bool ok = sock.waitForConnected(500);
    if (ok)
        sock.disconnectFromServer();
    return ok;
}

// ---------------------------------------------------------------------------
// Private helpers
// ---------------------------------------------------------------------------

QJsonObject BdfsClient::sendCommand(const QJsonObject &cmd) const
{
    QLocalSocket sock;
    sock.connectToServer(m_socketPath);
    if (!sock.waitForConnected(m_timeoutMs)) {
        qCWarning(lcBdfs) << "bdfs daemon not reachable at" << m_socketPath;
        return {};
    }

    const QByteArray payload = QJsonDocument(cmd).toJson(QJsonDocument::Compact) + '\n';
    sock.write(payload);
    if (!sock.waitForBytesWritten(m_timeoutMs)) {
        qCWarning(lcBdfs) << "bdfs: write timeout";
        return {};
    }

    if (!sock.waitForReadyRead(m_timeoutMs)) {
        qCWarning(lcBdfs) << "bdfs: read timeout";
        return {};
    }

    const QByteArray data = sock.readAll();
    sock.disconnectFromServer();

    QJsonParseError err;
    const QJsonDocument doc = QJsonDocument::fromJson(data, &err);
    if (err.error != QJsonParseError::NoError) {
        qCWarning(lcBdfs) << "bdfs: JSON parse error:" << err.errorString();
        return {};
    }
    return doc.object();
}

bool BdfsClient::isSuccess(const QJsonObject &response)
{
    return response.value(QStringLiteral("status")).toString() == QStringLiteral("ok");
}

// ---------------------------------------------------------------------------
// Queries
// ---------------------------------------------------------------------------

QStringList BdfsClient::listPartitions() const
{
    const QJsonObject resp = sendCommand({{"cmd", "list_partitions"}});
    QStringList result;
    for (const QJsonValue &v : resp.value(QStringLiteral("partitions")).toArray())
        result << v.toString();
    return result;
}

QStringList BdfsClient::listImages() const
{
    const QJsonObject resp = sendCommand({{"cmd", "list_images"}});
    QStringList result;
    for (const QJsonValue &v : resp.value(QStringLiteral("images")).toArray())
        result << v.toString();
    return result;
}

QStringList BdfsClient::listMounts() const
{
    const QJsonObject resp = sendCommand({{"cmd", "list_mounts"}});
    QStringList result;
    for (const QJsonValue &v : resp.value(QStringLiteral("mounts")).toArray())
        result << v.toString();
    return result;
}

QJsonObject BdfsClient::status() const
{
    return sendCommand({{"cmd", "status"}});
}

// ---------------------------------------------------------------------------
// Mount / unmount
// ---------------------------------------------------------------------------

bool BdfsClient::mountBlend(const QString &imagePath, const QString &subvolPath,
                             const QString &mountPoint, bool userspace)
{
    QJsonObject cmd{
        {"cmd", "blend_mount"},
        {"image", imagePath},
        {"subvol", subvolPath},
        {"mountpoint", mountPoint},
        {"userspace", userspace},
    };
    return isSuccess(sendCommand(cmd));
}

bool BdfsClient::umount(const QString &mountPoint)
{
    return isSuccess(sendCommand({{"cmd", "umount"}, {"mountpoint", mountPoint}}));
}

// ---------------------------------------------------------------------------
// Snapshot operations
// ---------------------------------------------------------------------------

bool BdfsClient::demote(const QString &subvolPath, const QString &outputPath)
{
    return isSuccess(sendCommand({
        {"cmd", "demote"},
        {"subvol", subvolPath},
        {"output", outputPath},
    }));
}

bool BdfsClient::importImage(const QString &imagePath, const QString &targetPath)
{
    return isSuccess(sendCommand({
        {"cmd", "import"},
        {"image", imagePath},
        {"target", targetPath},
    }));
}

bool BdfsClient::prune(const QString &subvolPath, int keep, const QString &pattern,
                        bool demoteFirst, bool dryRun)
{
    QJsonObject cmd{
        {"cmd", "prune"},
        {"subvol", subvolPath},
        {"keep", keep},
        {"demote_first", demoteFirst},
        {"dry_run", dryRun},
    };
    if (!pattern.isEmpty())
        cmd.insert(QStringLiteral("pattern"), pattern);
    return isSuccess(sendCommand(cmd));
}
