# SPDX-License-Identifier: GPL-2.0-or-later
#
# Top-level Makefile for the BTRFS+DwarFS framework.
#
# Targets:
#   all              Build kernel module + userspace (daemon + CLI)
#   kernel           Build only the kernel module
#   userspace        Build only the userspace tools
#   install          Install everything (requires root for kernel module)
#   install-autosnap Install the autosnap package-manager hook (opt-in)
#   uninstall-autosnap Remove autosnap files
#   clean            Remove all build artefacts
#   test             Run integration tests
#   check            Run unit tests (requires cmake -DBUILD_TESTS=ON)
#   fmt              Format C sources with clang-format
#
# Variables:
#   KDIR             Kernel source tree (default: running kernel)
#   PREFIX           Install prefix for userspace (default: /usr/local)
#   BUILD_DIR        CMake build directory (default: build/userspace)

KDIR      ?= /lib/modules/$(shell uname -r)/build
PREFIX    ?= /usr/local
BUILD_DIR ?= build/userspace

# autosnap install paths — co-located under /usr/lib/bdfs/autosnap/
AUTOSNAP_LIBDIR  ?= /usr/lib/bdfs/autosnap
AUTOSNAP_BINDIR  ?= $(PREFIX)/bin
AUTOSNAP_CONFDIR ?= /etc
AUTOSNAP_STATEDIR ?= /var/lib/autosnap

.PHONY: all kernel userspace install install-kernel install-userspace \
        install-autosnap uninstall-autosnap \
        clean clean-kernel clean-userspace test check fmt

all: kernel userspace

# ── Kernel module ────────────────────────────────────────────────────────────

kernel:
	$(MAKE) -C kernel KDIR=$(KDIR)

clean-kernel:
	$(MAKE) -C kernel clean

install-kernel: kernel
	$(MAKE) -C kernel install

# ── Userspace ────────────────────────────────────────────────────────────────

$(BUILD_DIR)/Makefile:
	cmake -S userspace -B $(BUILD_DIR) \
		-DCMAKE_BUILD_TYPE=Release \
		-DCMAKE_INSTALL_PREFIX=$(PREFIX) \
		-DBUILD_DAEMON=ON \
		-DBUILD_CLI=ON

userspace: $(BUILD_DIR)/Makefile
	cmake --build $(BUILD_DIR) --parallel

clean-userspace:
	rm -rf $(BUILD_DIR)

install-userspace: userspace
	cmake --install $(BUILD_DIR)

# ── Combined ─────────────────────────────────────────────────────────────────

install: install-kernel install-userspace install-post

# Post-install: refresh module dependencies, man page index, and systemd units.
# Each step is best-effort (|| true) so install succeeds on minimal systems
# that lack mandb or a running systemd.
install-post:
	depmod -a 2>/dev/null || true
	mandb -q 2>/dev/null || true
	@if command -v systemctl >/dev/null 2>&1 && \
	    systemctl is-system-running >/dev/null 2>&1; then \
	    systemctl daemon-reload; \
	    echo "systemd units reloaded"; \
	fi

# ── autosnap ─────────────────────────────────────────────────────────────────

install-autosnap:
	# Core library
	install -d $(DESTDIR)$(AUTOSNAP_LIBDIR)/backends
	install -d $(DESTDIR)$(AUTOSNAP_LIBDIR)/lib
	install -d $(DESTDIR)$(AUTOSNAP_STATEDIR)
	install -Dm644 tools/autosnap/lib/common.sh \
	    $(DESTDIR)$(AUTOSNAP_LIBDIR)/lib/common.sh
	install -Dm644 tools/autosnap/backends/bdfs.sh \
	    $(DESTDIR)$(AUTOSNAP_LIBDIR)/backends/bdfs.sh
	install -Dm644 tools/autosnap/backends/btrfs.sh \
	    $(DESTDIR)$(AUTOSNAP_LIBDIR)/backends/btrfs.sh
	install -Dm755 tools/autosnap/autosnap.py \
	    $(DESTDIR)$(AUTOSNAP_LIBDIR)/autosnap.py
	# Main dispatcher — patch LIBDIR and PYCORE paths
	install -Dm755 tools/autosnap/autosnap \
	    $(DESTDIR)$(AUTOSNAP_BINDIR)/autosnap
	sed -i \
	    -e 's|^AUTOSNAP_LIBDIR=.*|AUTOSNAP_LIBDIR="$(AUTOSNAP_LIBDIR)"|' \
	    -e 's|^AUTOSNAP_PYCORE=.*|AUTOSNAP_PYCORE="$(AUTOSNAP_LIBDIR)/autosnap.py"|' \
	    $(DESTDIR)$(AUTOSNAP_BINDIR)/autosnap
	# Config (do not overwrite existing)
	install -Dm644 tools/autosnap/autosnap.conf \
	    $(DESTDIR)$(AUTOSNAP_CONFDIR)/autosnap.conf
	# APT hook
	install -d $(DESTDIR)/etc/apt/apt.conf.d
	install -Dm644 tools/autosnap/hooks/apt/80-autosnap \
	    $(DESTDIR)/etc/apt/apt.conf.d/80-autosnap
	# DNF plugin
	install -d $(DESTDIR)$(PREFIX)/lib/python3/dist-packages/dnf-plugins
	install -Dm644 tools/autosnap/hooks/dnf/autosnap.py \
	    $(DESTDIR)$(PREFIX)/lib/python3/dist-packages/dnf-plugins/autosnap.py
	# Pacman hooks
	install -d $(DESTDIR)$(PREFIX)/share/libalpm/hooks
	install -Dm644 tools/autosnap/hooks/pacman/autosnap.hook \
	    $(DESTDIR)$(PREFIX)/share/libalpm/hooks/autosnap-pre.hook
	install -Dm644 tools/autosnap/hooks/pacman/autosnap-post.hook \
	    $(DESTDIR)$(PREFIX)/share/libalpm/hooks/autosnap-post.hook
	# Zypper plugin
	install -d $(DESTDIR)/usr/lib/zypp/plugins/commit
	install -Dm755 tools/autosnap/hooks/zypper/autosnap.py \
	    $(DESTDIR)/usr/lib/zypp/plugins/commit/autosnap
	@echo "autosnap installed. Edit $(AUTOSNAP_CONFDIR)/autosnap.conf to configure."
	@echo "Run 'autosnap detect' to verify backend detection."

uninstall-autosnap:
	rm -rf  $(DESTDIR)$(AUTOSNAP_LIBDIR)
	rm -f   $(DESTDIR)$(AUTOSNAP_BINDIR)/autosnap
	rm -f   $(DESTDIR)/etc/apt/apt.conf.d/80-autosnap
	rm -f   $(DESTDIR)$(PREFIX)/lib/python3/dist-packages/dnf-plugins/autosnap.py
	rm -f   $(DESTDIR)$(PREFIX)/share/libalpm/hooks/autosnap-pre.hook
	rm -f   $(DESTDIR)$(PREFIX)/share/libalpm/hooks/autosnap-post.hook
	rm -f   $(DESTDIR)/usr/lib/zypp/plugins/commit/autosnap
	@echo "autosnap uninstalled."
	@echo "Config $(AUTOSNAP_CONFDIR)/autosnap.conf and state $(AUTOSNAP_STATEDIR) were NOT removed."

clean: clean-kernel clean-userspace

# ── Tests ────────────────────────────────────────────────────────────────────

test: userspace
	@echo "Running integration tests..."
	bash tests/integration/run_all.sh

check:
	cmake -S userspace -B build/tests \
		-DCMAKE_BUILD_TYPE=Debug \
		-DBUILD_TESTS=ON \
		-DENABLE_ASAN=ON
	cmake --build build/tests --parallel
	cd build/tests && ctest --output-on-failure

# ── Formatting ───────────────────────────────────────────────────────────────

fmt:
	find kernel userspace include -name '*.c' -o -name '*.h' | \
		xargs clang-format -i --style=file 2>/dev/null || \
		find kernel userspace include -name '*.c' -o -name '*.h' | \
		xargs clang-format -i --style="{BasedOnStyle: Linux, IndentWidth: 8}"
