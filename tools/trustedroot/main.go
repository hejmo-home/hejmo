// Command trustedroot writes the Sigstore public-good trusted root (the
// Fulcio certificate authorities, the Rekor and certificate transparency log
// keys and the timestamp authorities) to the file given as its only argument.
//
// The box checks an uploaded update file against this root without any
// connection (ticket 221), so the file is part of the source tree:
//
//	go run ./tools/trustedroot internal/update/trusted_root.json
//
// The root is fetched with the library's TUF client, which starts from the
// TUF root metadata built into sigstore-go and verifies every step. Run it
// again when the Sigstore project rotates its keys (a test fails when the
// copy in install.sh differs from the one in the box) and review the diff.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sigstore/sigstore-go/pkg/root"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: trustedroot FILE")
		os.Exit(2)
	}
	tr, err := root.FetchTrustedRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "trustedroot:", err)
		os.Exit(1)
	}
	data, err := tr.MarshalJSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, "trustedroot:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Clean(os.Args[1]), append(data, '\n'), 0o644); err != nil { // #nosec G703 -- the path is the operator's own command-line argument of a build tool
		fmt.Fprintln(os.Stderr, "trustedroot:", err)
		os.Exit(1)
	}
}
