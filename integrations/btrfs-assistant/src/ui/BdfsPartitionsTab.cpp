#include "BdfsPartitionsTab.h"
#include "util/BdfsClient.h"

#include <QCheckBox>
#include <QComboBox>
#include <QFileDialog>
#include <QFormLayout>
#include <QGroupBox>
#include <QHBoxLayout>
#include <QLabel>
#include <QLineEdit>
#include <QListWidget>
#include <QMessageBox>
#include <QPushButton>
#include <QSpinBox>
#include <QSplitter>
#include <QTabWidget>
#include <QVBoxLayout>

BdfsPartitionsTab::BdfsPartitionsTab(BdfsClient *client, QWidget *parent)
    : QWidget(parent), m_client(client)
{
    setupUi();
    connectSignals();
    refresh();
}

// ---------------------------------------------------------------------------
// UI construction
// ---------------------------------------------------------------------------

void BdfsPartitionsTab::setupUi()
{
    auto *rootLayout = new QVBoxLayout(this);

    // Status bar
    m_statusLabel = new QLabel(tr("Checking bdfs daemon…"), this);
    rootLayout->addWidget(m_statusLabel);

    // Refresh button
    m_refreshBtn = new QPushButton(tr("Refresh"), this);
    rootLayout->addWidget(m_refreshBtn);

    // Inner tab widget
    auto *tabs = new QTabWidget(this);
    rootLayout->addWidget(tabs);

    // ---- Tab 1: Overview ------------------------------------------------
    auto *overviewWidget = new QWidget;
    auto *overviewLayout = new QHBoxLayout(overviewWidget);

    auto *partitionsGroup = new QGroupBox(tr("BTRFS Partitions"));
    auto *partitionsLayout = new QVBoxLayout(partitionsGroup);
    m_partitionsList = new QListWidget;
    partitionsLayout->addWidget(m_partitionsList);
    overviewLayout->addWidget(partitionsGroup);

    auto *imagesGroup = new QGroupBox(tr("DwarFS Images"));
    auto *imagesLayout = new QVBoxLayout(imagesGroup);
    m_imagesList = new QListWidget;
    imagesLayout->addWidget(m_imagesList);
    overviewLayout->addWidget(imagesGroup);

    auto *mountsGroup = new QGroupBox(tr("Active Blend Mounts"));
    auto *mountsLayout = new QVBoxLayout(mountsGroup);
    m_mountsList = new QListWidget;
    mountsLayout->addWidget(m_mountsList);
    overviewLayout->addWidget(mountsGroup);

    tabs->addTab(overviewWidget, tr("Overview"));

    // ---- Tab 2: Blend Mount / Unmount -----------------------------------
    auto *blendWidget = new QWidget;
    auto *blendLayout = new QFormLayout(blendWidget);

    m_blendImageEdit = new QLineEdit;
    m_blendImageEdit->setPlaceholderText(tr("/path/to/image.dwarfs"));
    blendLayout->addRow(tr("DwarFS image:"), m_blendImageEdit);

    m_blendSubvolEdit = new QLineEdit;
    m_blendSubvolEdit->setPlaceholderText(tr("/mnt/btrfs/subvol"));
    blendLayout->addRow(tr("BTRFS subvolume:"), m_blendSubvolEdit);

    m_blendMountpointEdit = new QLineEdit;
    m_blendMountpointEdit->setPlaceholderText(tr("/mnt/blend"));
    blendLayout->addRow(tr("Mount point:"), m_blendMountpointEdit);

    m_blendUserspaceCheck = new QCheckBox(tr("Use fuse-overlayfs fallback (userspace)"));
    blendLayout->addRow(QString(), m_blendUserspaceCheck);

    auto *blendBtnRow = new QHBoxLayout;
    m_mountBlendBtn = new QPushButton(tr("Mount Blend"));
    m_umountBtn = new QPushButton(tr("Unmount"));
    blendBtnRow->addWidget(m_mountBlendBtn);
    blendBtnRow->addWidget(m_umountBtn);
    blendBtnRow->addStretch();
    blendLayout->addRow(blendBtnRow);

    tabs->addTab(blendWidget, tr("Blend Mount"));

    // ---- Tab 3: Demote --------------------------------------------------
    auto *demoteWidget = new QWidget;
    auto *demoteLayout = new QFormLayout(demoteWidget);

    m_demoteSubvolEdit = new QLineEdit;
    m_demoteSubvolEdit->setPlaceholderText(tr("/mnt/btrfs/snapshot"));
    demoteLayout->addRow(tr("Snapshot subvolume:"), m_demoteSubvolEdit);

    m_demoteOutputEdit = new QLineEdit;
    m_demoteOutputEdit->setPlaceholderText(tr("/var/lib/bdfs/archives/snapshot.dwarfs"));
    demoteLayout->addRow(tr("Output .dwarfs path:"), m_demoteOutputEdit);

    m_demoteBtn = new QPushButton(tr("Demote to DwarFS"));
    demoteLayout->addRow(m_demoteBtn);

    tabs->addTab(demoteWidget, tr("Demote"));

    // ---- Tab 4: Import --------------------------------------------------
    auto *importWidget = new QWidget;
    auto *importLayout = new QFormLayout(importWidget);

    m_importImageEdit = new QLineEdit;
    m_importImageEdit->setPlaceholderText(tr("/path/to/image.dwarfs"));
    importLayout->addRow(tr("DwarFS image:"), m_importImageEdit);

    m_importTargetEdit = new QLineEdit;
    m_importTargetEdit->setPlaceholderText(tr("/mnt/btrfs/restored-subvol"));
    importLayout->addRow(tr("Target subvolume:"), m_importTargetEdit);

    m_importBtn = new QPushButton(tr("Import as BTRFS Subvolume"));
    importLayout->addRow(m_importBtn);

    tabs->addTab(importWidget, tr("Import"));

    // ---- Tab 5: Prune ---------------------------------------------------
    auto *pruneWidget = new QWidget;
    auto *pruneLayout = new QFormLayout(pruneWidget);

    m_pruneSubvolEdit = new QLineEdit;
    m_pruneSubvolEdit->setPlaceholderText(tr("/mnt/btrfs/subvol"));
    pruneLayout->addRow(tr("Subvolume:"), m_pruneSubvolEdit);

    m_pruneKeepSpin = new QSpinBox;
    m_pruneKeepSpin->setRange(1, 9999);
    m_pruneKeepSpin->setValue(5);
    pruneLayout->addRow(tr("Keep N most recent:"), m_pruneKeepSpin);

    m_prunePatternEdit = new QLineEdit;
    m_prunePatternEdit->setPlaceholderText(tr("* (leave empty for all)"));
    pruneLayout->addRow(tr("Name pattern:"), m_prunePatternEdit);

    m_pruneDemoteFirstCheck = new QCheckBox(tr("Demote to DwarFS before deleting"));
    pruneLayout->addRow(QString(), m_pruneDemoteFirstCheck);

    m_pruneDryRunCheck = new QCheckBox(tr("Dry run (report only, no changes)"));
    pruneLayout->addRow(QString(), m_pruneDryRunCheck);

    m_pruneBtn = new QPushButton(tr("Prune Snapshots"));
    pruneLayout->addRow(m_pruneBtn);

    tabs->addTab(pruneWidget, tr("Prune"));
}

void BdfsPartitionsTab::connectSignals()
{
    connect(m_refreshBtn, &QPushButton::clicked, this, &BdfsPartitionsTab::refresh);
    connect(m_mountBlendBtn, &QPushButton::clicked, this, &BdfsPartitionsTab::onMountBlend);
    connect(m_umountBtn, &QPushButton::clicked, this, &BdfsPartitionsTab::onUmount);
    connect(m_demoteBtn, &QPushButton::clicked, this, &BdfsPartitionsTab::onDemote);
    connect(m_importBtn, &QPushButton::clicked, this, &BdfsPartitionsTab::onImport);
    connect(m_pruneBtn, &QPushButton::clicked, this, &BdfsPartitionsTab::onPrune);
}

// ---------------------------------------------------------------------------
// Slots
// ---------------------------------------------------------------------------

void BdfsPartitionsTab::refresh()
{
    const bool available = m_client->isAvailable();
    setDaemonAvailable(available);
    if (!available)
        return;

    m_partitionsList->clear();
    m_partitionsList->addItems(m_client->listPartitions());

    m_imagesList->clear();
    m_imagesList->addItems(m_client->listImages());

    m_mountsList->clear();
    m_mountsList->addItems(m_client->listMounts());
}

void BdfsPartitionsTab::setDaemonAvailable(bool available)
{
    if (available) {
        m_statusLabel->setText(tr("bdfs daemon: connected"));
        m_statusLabel->setStyleSheet(QStringLiteral("color: green"));
    } else {
        m_statusLabel->setText(tr("bdfs daemon: not running"));
        m_statusLabel->setStyleSheet(QStringLiteral("color: red"));
    }
}

void BdfsPartitionsTab::onMountBlend()
{
    const QString image = m_blendImageEdit->text().trimmed();
    const QString subvol = m_blendSubvolEdit->text().trimmed();
    const QString mp = m_blendMountpointEdit->text().trimmed();

    if (image.isEmpty() || subvol.isEmpty() || mp.isEmpty()) {
        QMessageBox::warning(this, tr("Missing fields"),
                             tr("Image path, subvolume, and mount point are required."));
        return;
    }

    const bool ok = m_client->mountBlend(image, subvol, mp, m_blendUserspaceCheck->isChecked());
    if (ok) {
        QMessageBox::information(this, tr("Success"), tr("Blend mount created at %1.").arg(mp));
        refresh();
    } else {
        QMessageBox::critical(this, tr("Error"), tr("Failed to mount blend layer. Check the bdfs daemon log."));
    }
}

void BdfsPartitionsTab::onUmount()
{
    const QString mp = m_blendMountpointEdit->text().trimmed();
    if (mp.isEmpty()) {
        QMessageBox::warning(this, tr("Missing field"), tr("Enter the mount point to unmount."));
        return;
    }

    const bool ok = m_client->umount(mp);
    if (ok) {
        QMessageBox::information(this, tr("Success"), tr("Unmounted %1.").arg(mp));
        refresh();
    } else {
        QMessageBox::critical(this, tr("Error"), tr("Failed to unmount. Check the bdfs daemon log."));
    }
}

void BdfsPartitionsTab::onDemote()
{
    const QString subvol = m_demoteSubvolEdit->text().trimmed();
    const QString output = m_demoteOutputEdit->text().trimmed();

    if (subvol.isEmpty() || output.isEmpty()) {
        QMessageBox::warning(this, tr("Missing fields"),
                             tr("Snapshot subvolume and output path are required."));
        return;
    }

    const bool ok = m_client->demote(subvol, output);
    if (ok) {
        QMessageBox::information(this, tr("Success"),
                                 tr("Demoted %1 to %2.").arg(subvol, output));
        refresh();
    } else {
        QMessageBox::critical(this, tr("Error"), tr("Demote failed. Check the bdfs daemon log."));
    }
}

void BdfsPartitionsTab::onImport()
{
    const QString image = m_importImageEdit->text().trimmed();
    const QString target = m_importTargetEdit->text().trimmed();

    if (image.isEmpty() || target.isEmpty()) {
        QMessageBox::warning(this, tr("Missing fields"),
                             tr("Image path and target subvolume are required."));
        return;
    }

    const bool ok = m_client->importImage(image, target);
    if (ok) {
        QMessageBox::information(this, tr("Success"),
                                 tr("Imported %1 as %2.").arg(image, target));
        refresh();
    } else {
        QMessageBox::critical(this, tr("Error"), tr("Import failed. Check the bdfs daemon log."));
    }
}

void BdfsPartitionsTab::onPrune()
{
    const QString subvol = m_pruneSubvolEdit->text().trimmed();
    if (subvol.isEmpty()) {
        QMessageBox::warning(this, tr("Missing field"), tr("Subvolume path is required."));
        return;
    }

    const int keep = m_pruneKeepSpin->value();
    const QString pattern = m_prunePatternEdit->text().trimmed();
    const bool demoteFirst = m_pruneDemoteFirstCheck->isChecked();
    const bool dryRun = m_pruneDryRunCheck->isChecked();

    if (!dryRun) {
        const auto answer = QMessageBox::question(
            this, tr("Confirm prune"),
            tr("Prune snapshots of %1, keeping %2 most recent%3?")
                .arg(subvol)
                .arg(keep)
                .arg(demoteFirst ? tr(" (demote first)") : QString()));
        if (answer != QMessageBox::Yes)
            return;
    }

    const bool ok = m_client->prune(subvol, keep, pattern, demoteFirst, dryRun);
    if (ok) {
        QMessageBox::information(this, tr("Success"),
                                 dryRun ? tr("Dry run complete. Check daemon log for details.")
                                        : tr("Prune complete."));
        refresh();
    } else {
        QMessageBox::critical(this, tr("Error"), tr("Prune failed. Check the bdfs daemon log."));
    }
}
