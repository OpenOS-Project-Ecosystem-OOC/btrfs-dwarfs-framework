// SPDX-License-Identifier: GPL-2.0-or-later
//
// bdfs-pin-helper — CLI wrapper around dwarfspin.Pinner.
//
// NOTE: The canonical source lives in integrations/gitlab-enhanced at
// tools/bdfs-pin-helper/main.go. This copy is kept in sync for reference.
// Build and install via:
//
//	make install-pin-helper          (uses integrations/gitlab-enhanced)
//	make install-gitlab-enhanced     (builds everything)
//
// Called by bdfs_daemon (C) and bdfs_transfer.py (Python/btr-fs-git) to pin,
// query, list, and unpin DwarFS archives via the Kubo HTTP API.
//
// Modes (mutually exclusive flags):
//
//	Pin (default):
//	  bdfs-pin-helper --archive <path> --kubo-api <url>
//	                  [--index <db>] [--chunk-kb <n>] [--timeout <s>]
//	  Output: {"cid":"<CID>","cached":true|false,"size_bytes":<n>}
//
//	Stat (query index, no Kubo contact):
//	  bdfs-pin-helper --stat --archive <path> [--index <db>]
//	  Output: {"cid":"<CID>","path":"<path>"}
//
//	List (dump all index entries):
//	  bdfs-pin-helper --list [--index <db>]
//	  Output: one JSON object per line: {"path":"...","cid":"...","pinned_at":"<RFC3339>"}
//
//	Unpin (remove pin + index entry):
//	  bdfs-pin-helper --unpin --archive <path> --kubo-api <url> [--index <db>]
//	  Output: {"unpinned":true,"cid":"<CID>"}
//
// Exit codes:
//
//	0  success
//	1  operation failed
//	2  bad arguments

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	dwarfspin "gitlab.com/openos-project/git-management_deving/gitlab-enhanced/ipfs/dwarfs-pin"
)

func main() {
	archive    := flag.String("archive",  "",    "path to .dwarfs archive")
	kuboAPI    := flag.String("kubo-api", "",    "Kubo HTTP API URL, e.g. http://127.0.0.1:5001")
	indexPath  := flag.String("index",    "",    "SQLite index path (default: /var/lib/gitlab-enhanced/dwarfs-pin.db)")
	chunkKB    := flag.Int("chunk-kb",    0,     "CAR chunk size in KiB (0 = default 256 KiB)")
	timeoutSec := flag.Int("timeout",     1800,  "pin timeout in seconds")
	doStat     := flag.Bool("stat",       false, "query index for --archive and print CID")
	doList     := flag.Bool("list",       false, "print all index entries as JSON lines")
	doUnpin    := flag.Bool("unpin",      false, "unpin --archive from IPFS and remove index entry")
	flag.Parse()

	idx := *indexPath
	if idx == "" {
		idx = "/var/lib/gitlab-enhanced/dwarfs-pin.db"
	}

	// ── --stat ────────────────────────────────────────────────────────────
	if *doStat {
		if *archive == "" {
			fmt.Fprintln(os.Stderr, "bdfs-pin-helper: --stat requires --archive")
			os.Exit(2)
		}
		index, err := dwarfspin.OpenIndex(idx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bdfs-pin-helper: open index: %v\n", err)
			os.Exit(1)
		}
		defer index.Close() //nolint:errcheck
		cid, err := index.Lookup(*archive)
		if err != nil || cid == "" {
			fmt.Fprintf(os.Stderr, "bdfs-pin-helper: %s not in index\n", *archive)
			os.Exit(1)
		}
		out, _ := json.Marshal(map[string]string{"cid": cid, "path": *archive})
		fmt.Printf("%s\n", out)
		return
	}

	// ── --list ────────────────────────────────────────────────────────────
	if *doList {
		index, err := dwarfspin.OpenIndex(idx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bdfs-pin-helper: open index: %v\n", err)
			os.Exit(1)
		}
		defer index.Close() //nolint:errcheck
		entries, err := index.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "bdfs-pin-helper: list index: %v\n", err)
			os.Exit(1)
		}
		for _, e := range entries {
			out, _ := json.Marshal(map[string]string{
				"path":      e.Path,
				"cid":       e.CID,
				"pinned_at": e.PinnedAt.UTC().Format(time.RFC3339),
			})
			fmt.Printf("%s\n", out)
		}
		return
	}

	// ── --unpin ───────────────────────────────────────────────────────────
	if *doUnpin {
		if *archive == "" || *kuboAPI == "" {
			fmt.Fprintln(os.Stderr, "bdfs-pin-helper: --unpin requires --archive and --kubo-api")
			os.Exit(2)
		}
		pinner, err := dwarfspin.New(dwarfspin.Config{
			Enabled:     true,
			KuboAPI:     *kuboAPI,
			IndexPath:   idx,
			ChunkSizeKB: *chunkKB,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "bdfs-pin-helper: init pinner: %v\n", err)
			os.Exit(1)
		}
		if pinner != nil {
			defer pinner.Close() //nolint:errcheck
		}
		cid, _ := pinner.Stat(*archive)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := pinner.Unpin(ctx, *archive); err != nil {
			fmt.Fprintf(os.Stderr, "bdfs-pin-helper: unpin %s: %v\n", *archive, err)
			os.Exit(1)
		}
		out, _ := json.Marshal(map[string]interface{}{"unpinned": true, "cid": cid})
		fmt.Printf("%s\n", out)
		return
	}

	// ── pin (default) ─────────────────────────────────────────────────────
	if *archive == "" || *kuboAPI == "" {
		fmt.Fprintln(os.Stderr, "bdfs-pin-helper: --archive and --kubo-api are required for pinning")
		fmt.Fprintln(os.Stderr, "Use --stat, --list, or --unpin for other operations.")
		os.Exit(2)
	}

	pinner, err := dwarfspin.New(dwarfspin.Config{
		Enabled:     true,
		KuboAPI:     *kuboAPI,
		IndexPath:   idx,
		ChunkSizeKB: *chunkKB,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "bdfs-pin-helper: init pinner: %v\n", err)
		os.Exit(1)
	}
	if pinner != nil {
		defer pinner.Close() //nolint:errcheck
	}

	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(*timeoutSec)*time.Second)
	defer cancel()

	result, err := pinner.Pin(ctx, *archive)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bdfs-pin-helper: pin %s: %v\n", *archive, err)
		os.Exit(1)
	}

	out, _ := json.Marshal(map[string]interface{}{
		"cid":        result.CID,
		"cached":     result.Cached,
		"size_bytes": result.SizeBytes,
	})
	fmt.Printf("%s\n", out)
}
