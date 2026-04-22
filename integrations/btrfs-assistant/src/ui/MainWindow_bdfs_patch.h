/**
 * MainWindow_bdfs_patch.h — instructions for wiring BdfsPartitionsTab into MainWindow
 *
 * This file is NOT compiled directly.  It documents the minimal changes
 * needed in MainWindow.h and MainWindow.cpp to integrate the BDFS tab.
 *
 * ============================================================
 * MainWindow.h changes
 * ============================================================
 *
 * 1. Forward-declare BdfsClient and BdfsPartitionsTab after the existing
 *    forward declarations:
 *
 *      class BdfsClient;
 *      class BdfsPartitionsTab;
 *
 * 2. Add private members (after the existing private members):
 *
 *      BdfsClient       *m_bdfsClient       = nullptr;
 *      BdfsPartitionsTab *m_bdfsPartitionsTab = nullptr;
 *
 * ============================================================
 * MainWindow.cpp changes
 * ============================================================
 *
 * 1. Add includes near the top:
 *
 *      #include "BdfsPartitionsTab.h"
 *      #include "util/BdfsClient.h"
 *
 * 2. In the MainWindow constructor, after the existing tab setup, add:
 *
 *      // BDFS tab — gracefully absent when daemon is not running
 *      m_bdfsClient = new BdfsClient(this);
 *      m_bdfsPartitionsTab = new BdfsPartitionsTab(m_bdfsClient, this);
 *      ui->tabWidget->addTab(m_bdfsPartitionsTab, tr("BDFS"));
 *
 * That is all.  BdfsPartitionsTab handles its own refresh and disables
 * itself when the daemon is unreachable.
 */

// This header intentionally contains no compilable code.
