// Command mkupdate builds the update files of a release (ticket 221): the
// files a family in offline mode uploads to its box. The release workflow runs
// it in three steps for each architecture, with cosign in between:
//
//	mkupdate manifest -version 0.2.4 -arch arm64 -image image.tar -out hejmo-update.json
//	cosign sign-blob --new-bundle-format --bundle hejmo-update.sigstore.json hejmo-update.json
//	mkupdate pack -manifest hejmo-update.json -bundle hejmo-update.sigstore.json -image image.tar -out hejmo-0.2.4-arm64.update
//	mkupdate check -arch arm64 hejmo-0.2.4-arm64.update
//
// The last step checks the file the way the box does, offline, against the
// Sigstore root built into the box: a release whose files the box would refuse
// does not go out.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/hejmo-home/hejmo/internal/update"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, nil); err != nil {
		fmt.Fprintln(os.Stderr, "mkupdate:", err)
		os.Exit(1)
	}
}

// run is main with its streams and the verifier of "check" (nil: the box's own).
func run(args []string, out io.Writer, v update.Verifier) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mkupdate manifest|pack|check ...")
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	switch args[0] {
	case "manifest":
		version := fs.String("version", "", "the version, x.y.z")
		arch := fs.String("arch", "", "arm64 or amd64")
		image := fs.String("image", "", "the image archive (docker save)")
		outFile := fs.String("out", "", "where the manifest goes")
		released := fs.String("released", time.Now().UTC().Format("2006-01-02"), "release date, YYYY-MM-DD")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		m, err := update.NewManifest(*version, *arch, *released, *image)
		if err != nil {
			return err
		}
		data, err := update.MarshalManifest(m)
		if err != nil {
			return err
		}
		return os.WriteFile(*outFile, data, 0o644)
	case "pack":
		manifest := fs.String("manifest", "", "the signed manifest")
		bundle := fs.String("bundle", "", "the cosign bundle that signs it")
		image := fs.String("image", "", "the image archive")
		outFile := fs.String("out", "", "the update file")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		m, err := os.ReadFile(*manifest)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(*bundle)
		if err != nil {
			return err
		}
		img, err := os.Open(*image)
		if err != nil {
			return err
		}
		defer img.Close()
		st, err := img.Stat()
		if err != nil {
			return err
		}
		dst, err := os.Create(*outFile)
		if err != nil {
			return err
		}
		if err := update.Pack(dst, m, b, img, st.Size()); err != nil {
			dst.Close()
			return err
		}
		return dst.Close()
	case "check":
		arch := fs.String("arch", "", "the architecture the file is for")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return fmt.Errorf("usage: mkupdate check -arch ARCH FILE")
		}
		if v == nil {
			d, err := update.DefaultVerifier()
			if err != nil {
				return err
			}
			v = d
		}
		info, err := update.Inspect(fs.Arg(0), update.Check{Arch: *arch, Verify: v})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: Hejmo %s for %s, %d bytes, sha256 %s, signed by %s\n", fs.Arg(0), info.Version, info.Arch, info.Size, info.SHA256, info.Signer)
		return nil
	}
	return fmt.Errorf("unknown command %q", args[0])
}
