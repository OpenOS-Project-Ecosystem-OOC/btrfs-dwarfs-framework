// SPDX-License-Identifier: GPL-2.0-or-later
//
// bdfs-pin-helper — thin CLI wrapper around dwarfspin.Pinner.
//
// Called by the bdfs daemon after a successful workspace demote to pin the
// resulting DwarFS archive to IPFS. Keeping this as a separate process avoids
// CGo and lets the C daemon stay dependency-free.
//
// Usage:
//
//	bdfs-pin-helper --archive <path> --kubo-api <url> --index <db-path>
//	                [--chunk-size-kb <n>] [--timeout <seconds>]
//
// Output (stdout, one JSON line):
//
//	{"cid":"<CID>","cached":true|false,"size_bytes":<n>}
//
// Exit codes:
//
//	0  pinned successfully (or already cached)
//	1  pin failed
//	2  bad arguments

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	dwarfspin "gitlab.com/openos-project/linux-kernel_filesystem_deving/btrfs-dwarfs-framework/ipfs/dwarfs-pin"
)

func main() {
	archive    := flag.String("archive",      "",    "path to .dwarfs archive (required)")
	kuboAPI    := flag.String("kubo-api",     "",    "Kubo HTTP API URL, e.g. http://127.0.0.1:5001 (required)")
	indexPath  := flag.String("index",        "",    "SQLite index path (default: /var/lib/bdfs/dwarfs-pin.db)")
	chunkKB    := flag.Int("chunk-size-kb",   256,   "CAR chunk size in KiB")
	timeoutSec := flag.Int("timeout",         1800,  "pin timeout in seconds")
	flag.Parse()

	if *archive == "" || *kuboAPI == "" {
		fmt.Fprintln(os.Stderr, "bdfs-pin-helper: --archive and --kubo-api are required")
		os.Exit(2)
	}

	idx := *indexPath
	if idx == "" {
		idx = "/var/lib/bdfs/dwarfs-pin.db"
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
