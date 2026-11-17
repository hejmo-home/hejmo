package outbound_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The one place for every connection to the outside is package outbound
// (ticket 201, Q144). This test is the check that no other code goes around
// it: it parses every Go file of the box and fails on a default HTTP client, a
// hand-made client or transport and a raw dial or lookup anywhere else.

// forbidden lists, per import path, what only package outbound may use.
var forbidden = map[string][]string{
	"net/http": {"Get", "Head", "Post", "PostForm", "DefaultClient", "DefaultTransport", "Transport"},
	"net": {"Dial", "DialTimeout", "DialTCP", "DialUDP", "DialIP", "DialUnix", "Dialer",
		"LookupHost", "LookupIP", "LookupAddr", "LookupCNAME", "LookupMX", "LookupNS", "LookupPort", "LookupSRV", "LookupTXT", "Resolver", "DefaultResolver"},
	"crypto/tls": {"Dial", "DialWithDialer", "Dialer"},
}

// forbiddenImports are libraries that open connections of their own; only the
// package of the DNS service, whose operations pass the gate, may use them.
var forbiddenImports = []string{"github.com/miekg/dns", "github.com/caddyserver/certmagic", "github.com/mholt/acmez", "github.com/libdns/", "net/smtp", "net/rpc"}

// exempt lists directories that are not the box: the package itself, developer
// tools and test helpers. Test files are not checked.
var exempt = []string{"internal/outbound", "tools", "cmd/hejmo-measure", "internal/caldavclient/caldavtest"}

// importAllowed says which package may use a forbidden import.
func importAllowed(dir string) bool { return dir == "internal/publiccert" }

// bypasses returns what in the source of one file goes around package outbound.
func bypasses(fset *token.FileSet, f *ast.File, dir string) []string {
	var out []string
	names := map[string]string{} // local name -> import path
	for _, im := range f.Imports {
		p, _ := strconv.Unquote(im.Path.Value)
		name := filepath.Base(p)
		if im.Name != nil {
			name = im.Name.Name
		}
		names[name] = p
		for _, bad := range forbiddenImports {
			if (p == bad || strings.HasSuffix(bad, "/") && strings.HasPrefix(p, bad)) && !importAllowed(dir) {
				out = append(out, fset.Position(im.Pos()).String()+": imports "+p)
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			id, ok := x.X.(*ast.Ident)
			if !ok {
				return true
			}
			if slices.Contains(forbidden[names[id.Name]], x.Sel.Name) {
				out = append(out, fset.Position(x.Pos()).String()+": "+id.Name+"."+x.Sel.Name)
			}
		case *ast.CompositeLit:
			if sel, ok := x.Type.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && names[id.Name] == "net/http" && sel.Sel.Name == "Client" {
					out = append(out, fset.Position(x.Pos()).String()+": http.Client literal")
				}
			}
		}
		return true
	})
	return out
}

func TestNoCodeGoesAroundTheOutboundPackage(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && rel != "." || d.Name() == "node_modules" || slices.Contains(exempt, rel) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		found = append(found, bypasses(fset, f, filepath.ToSlash(filepath.Dir(rel)))...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range found {
		t.Errorf("goes around package outbound: %s (use outbound.Gate.Client, Check or outbound.Local)", b)
	}
}

func TestTheBypassCheckCatchesWhatItShould(t *testing.T) {
	bad := `package x
import (
	"net"
	"net/http"
	h2 "net/http"
	"github.com/miekg/dns"
)
func f() {
	http.Get("http://x")
	_ = http.DefaultClient
	_ = &http.Client{}
	_ = &h2.Transport{}
	net.Dial("tcp", "x:1")
	_ = &net.Dialer{}
	net.LookupHost("x")
	_ = dns.Client{}
}`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "bad.go", bad, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := bypasses(fset, f, "internal/server")
	for _, want := range []string{"http.Get", "http.DefaultClient", "http.Client literal", "h2.Transport", "net.Dial", "net.Dialer", "net.LookupHost", "imports github.com/miekg/dns"} {
		if !slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, want) }) {
			t.Errorf("the check missed %q; found %v", want, got)
		}
	}
	// A client that is only passed around is fine.
	ok := `package x
import "net/http"
type T struct{ c *http.Client }
func f(c *http.Client) *http.Request { r, _ := http.NewRequest("GET", "/", nil); return r }`
	f, _ = parser.ParseFile(fset, "ok.go", ok, 0)
	if got := bypasses(fset, f, "internal/server"); len(got) != 0 {
		t.Errorf("false alarm: %v", got)
	}
}
