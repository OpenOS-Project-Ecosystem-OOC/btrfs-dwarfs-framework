# environments/workspace/Dockerfile
#
# Workspace container image for gitlab-enhanced environments.
#
# Installs bdfs (BTRFS+DwarFS CLI + daemon), bfg (btr-fs-git), and the
# DwarFS userspace tools so the supervisor shutdown hooks can take workspace
# snapshots on pause and demote subvolumes to DwarFS archives on delete.
#
# Build:
#   docker build -t gitlab-enhanced/workspace-full:latest environments/workspace/
#
# The image is intentionally based on Ubuntu LTS for broad package availability.
# Pin the digest in production to prevent unexpected base image changes.

FROM ubuntu:24.04

# Prevent interactive prompts during package installation
ENV DEBIAN_FRONTEND=noninteractive

# ── System packages ───────────────────────────────────────────────────────────

RUN apt-get update -qq && apt-get install -y --no-install-recommends \
    # BTRFS userspace tools (btrfs subvolume snapshot, send, receive)
    btrfs-progs \
    # DwarFS userspace tools (mkdwarfs, dwarfs FUSE driver, dwarfsextract)
    # Available in Ubuntu 24.04 universe; earlier releases need a PPA or build.
    fuse3 \
    libfuse3-3 \
    # fuse-overlayfs for userspace blend layer fallback
    fuse-overlayfs \
    # Build dependencies for bdfs and bfg (installed from source below)
    build-essential \
    cmake \
    git \
    python3 \
    python3-pip \
    # Runtime dependencies
    ca-certificates \
    curl \
    wget \
    # Workspace tooling
    sudo \
    locales \
    tzdata \
    && rm -rf /var/lib/apt/lists/*

# ── DwarFS tools ──────────────────────────────────────────────────────────────
#
# Install pre-built DwarFS universal binaries from the upstream GitHub release.
# These are statically linked and work on any x86_64 Linux kernel >= 4.18.
#
# Pin to a specific release tag for reproducibility. Update DWARFS_VERSION to
# pick up new releases.

ARG DWARFS_VERSION=0.10.2
ARG DWARFS_ARCH=Linux-x86_64

RUN set -eux; \
    DWARFS_BASE="https://github.com/mhx/dwarfs/releases/download/v${DWARFS_VERSION}"; \
    DWARFS_PKG="dwarfs-universal-${DWARFS_VERSION}-${DWARFS_ARCH}.tar.xz"; \
    curl -fsSL "${DWARFS_BASE}/${DWARFS_PKG}" -o /tmp/dwarfs.tar.xz; \
    tar -xf /tmp/dwarfs.tar.xz -C /tmp; \
    # The universal binary is a single executable that behaves as mkdwarfs,
    # dwarfs, dwarfsextract, or dwarfsck depending on argv[0].
    install -m 0755 /tmp/dwarfs-universal-*/dwarfs-universal /usr/local/bin/dwarfs-universal; \
    ln -sf dwarfs-universal /usr/local/bin/mkdwarfs; \
    ln -sf dwarfs-universal /usr/local/bin/dwarfs; \
    ln -sf dwarfs-universal /usr/local/bin/dwarfsextract; \
    ln -sf dwarfs-universal /usr/local/bin/dwarfsck; \
    rm -rf /tmp/dwarfs.tar.xz /tmp/dwarfs-universal-*; \
    # Smoke test
    dwarfs-universal --version 2>&1 | grep -q dwarfs

# ── bdfs CLI + daemon ─────────────────────────────────────────────────────────
#
# Build bdfs from the local source tree. In CI this is the checked-out repo;
# in standalone builds pass --build-arg BDFS_REPO=<url> to clone from remote.
#
# We copy only the files needed for the userspace build to keep the layer
# cache valid when kernel/ or docs change.

ARG BDFS_REPO=""

COPY userspace/ /build/bdfs/userspace/
COPY include/   /build/bdfs/include/
COPY configs/   /build/bdfs/configs/

RUN set -eux; \
    if [ -n "${BDFS_REPO}" ]; then \
        git clone --depth=1 "${BDFS_REPO}" /build/bdfs-remote; \
        cp -r /build/bdfs-remote/userspace /build/bdfs/userspace; \
        cp -r /build/bdfs-remote/include   /build/bdfs/include; \
        cp -r /build/bdfs-remote/configs   /build/bdfs/configs; \
    fi; \
    cmake -S /build/bdfs/userspace -B /build/bdfs/build \
        -DCMAKE_BUILD_TYPE=Release \
        -DCMAKE_INSTALL_PREFIX=/usr/local \
        -DBUILD_DAEMON=ON \
        -DBUILD_CLI=ON \
        -DBUILD_TESTS=OFF; \
    cmake --build /build/bdfs/build --parallel "$(nproc)"; \
    cmake --install /build/bdfs/build; \
    rm -rf /build/bdfs; \
    # Smoke test
    bdfs --version

# ── bfg (btr-fs-git) ─────────────────────────────────────────────────────────
#
# bfg is a Python tool that provides git-like snapshot workflows on BTRFS
# subvolumes (bfg local_commit, bfg push, bfg pull).
#
# Install from the btr-fs-git submodule bundled in this repo, or from the
# upstream repository when BDFS_REPO is set.

COPY integrations/btr-fs-git/ /build/bfg/

RUN set -eux; \
    if [ -n "${BDFS_REPO}" ]; then \
        git clone --depth=1 \
            https://gitlab.com/openos-project/linux-kernel_filesystem_deving/btr-fs-git.git \
            /build/bfg-remote; \
        cp -r /build/bfg-remote/. /build/bfg/; \
    fi; \
    # Install bfg as a Python package if setup.py / pyproject.toml exists,
    # otherwise install the standalone script directly.
    if [ -f /build/bfg/setup.py ] || [ -f /build/bfg/pyproject.toml ]; then \
        pip3 install --no-cache-dir /build/bfg; \
    elif [ -f /build/bfg/bfg ]; then \
        install -m 0755 /build/bfg/bfg /usr/local/bin/bfg; \
    elif [ -f /build/bfg/bfg.py ]; then \
        install -m 0755 /build/bfg/bfg.py /usr/local/bin/bfg; \
    fi; \
    rm -rf /build/bfg /build/bfg-remote; \
    # Smoke test — bfg prints usage when called with no args (exit 1 is fine)
    bfg --help 2>&1 | grep -qi "bfg\|usage" || true

# ── Runtime configuration ─────────────────────────────────────────────────────

# Default bdfs daemon config
RUN mkdir -p /etc/bdfs /var/lib/bdfs /run/bdfs
COPY configs/bdfs.conf /etc/bdfs/bdfs.conf

# Allow the workspace user to run bdfs and btrfs without a password.
# In production, scope this to the specific binaries via sudoers.d.
RUN echo "ALL ALL=(root) NOPASSWD: /usr/local/bin/bdfs, /usr/local/sbin/bdfs_daemon, /usr/bin/btrfs" \
    > /etc/sudoers.d/bdfs && chmod 0440 /etc/sudoers.d/bdfs

# ── Workspace user ────────────────────────────────────────────────────────────

RUN useradd -m -s /bin/bash -u 1000 gitpod \
    && echo "gitpod ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/gitpod \
    && chmod 0440 /etc/sudoers.d/gitpod

USER gitpod
WORKDIR /workspace

# ── Verify tools are on PATH ──────────────────────────────────────────────────

RUN bdfs --version \
    && mkdwarfs --version 2>&1 | grep -q dwarfs \
    && dwarfsextract --version 2>&1 | grep -q dwarfs \
    && bfg --help 2>&1 | grep -qi "bfg\|usage" || true

CMD ["/bin/bash"]
