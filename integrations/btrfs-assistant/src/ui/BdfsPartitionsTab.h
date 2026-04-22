#ifndef BDFSPARTITIONSTAB_H
#define BDFSPARTITIONSTAB_H

#include <QWidget>

class QComboBox;
class QLabel;
class QLineEdit;
class QListWidget;
class QPushButton;
class QCheckBox;
class QSpinBox;
class QTabWidget;

class BdfsClient;

/**
 * @brief Tab widget providing a GUI for btrfs-dwarfs-framework operations.
 *
 * Covers:
 *  - Listing BTRFS partitions and DwarFS images known to the daemon
 *  - Mounting / unmounting blend layers
 *  - Demoting snapshots to DwarFS images
 *  - Importing DwarFS images as BTRFS subvolumes
 *  - Pruning snapshots with optional DwarFS archival
 *
 * The tab disables itself gracefully when the bdfs daemon is not running.
 */
class BdfsPartitionsTab : public QWidget {
    Q_OBJECT

  public:
    explicit BdfsPartitionsTab(BdfsClient *client, QWidget *parent = nullptr);

  public slots:
    /** @brief Refresh all lists from the daemon. */
    void refresh();

  private slots:
    void onMountBlend();
    void onUmount();
    void onDemote();
    void onImport();
    void onPrune();

  private:
    void setupUi();
    void connectSignals();
    void setDaemonAvailable(bool available);

    BdfsClient *m_client = nullptr;

    // Status
    QLabel *m_statusLabel = nullptr;

    // Partitions / images / mounts lists
    QListWidget *m_partitionsList = nullptr;
    QListWidget *m_imagesList = nullptr;
    QListWidget *m_mountsList = nullptr;

    // Blend mount controls
    QLineEdit *m_blendImageEdit = nullptr;
    QLineEdit *m_blendSubvolEdit = nullptr;
    QLineEdit *m_blendMountpointEdit = nullptr;
    QCheckBox *m_blendUserspaceCheck = nullptr;
    QPushButton *m_mountBlendBtn = nullptr;
    QPushButton *m_umountBtn = nullptr;

    // Demote controls
    QLineEdit *m_demoteSubvolEdit = nullptr;
    QLineEdit *m_demoteOutputEdit = nullptr;
    QPushButton *m_demoteBtn = nullptr;

    // Import controls
    QLineEdit *m_importImageEdit = nullptr;
    QLineEdit *m_importTargetEdit = nullptr;
    QPushButton *m_importBtn = nullptr;

    // Prune controls
    QLineEdit *m_pruneSubvolEdit = nullptr;
    QSpinBox *m_pruneKeepSpin = nullptr;
    QLineEdit *m_prunePatternEdit = nullptr;
    QCheckBox *m_pruneDemoteFirstCheck = nullptr;
    QCheckBox *m_pruneDryRunCheck = nullptr;
    QPushButton *m_pruneBtn = nullptr;

    // Refresh
    QPushButton *m_refreshBtn = nullptr;
};

#endif // BDFSPARTITIONSTAB_H
