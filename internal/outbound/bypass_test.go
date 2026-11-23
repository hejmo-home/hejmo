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

// outboundPath is package outbound's import path.
const outboundPath = "github.com/hejmo-home/hejmo/internal/outbound"

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
	isOutbound := func(e ast.Expr) (*ast.SelectorExpr, bool) {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			return nil, false
		}
		id, ok := sel.X.(*ast.Ident)
		return sel, ok && names[id.Name] == outboundPath
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
			if names[id.Name] == outboundPath && x.Sel.Name == "AllowAll" {
				out = append(out, fset.Position(x.Pos()).String()+": outbound.AllowAll (for tests only)")
			}
		case *ast.CompositeLit:
			if sel, ok := x.Type.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && names[id.Name] == "net/http" && sel.Sel.Name == "Client" {
					out = append(out, fset.Position(x.Pos()).String()+": http.Client literal")
				}
				if o, ok := isOutbound(sel); ok && o.Sel.Name == "Gate" {
					out = append(out, fset.Position(x.Pos()).String()+": outbound.Gate literal (a Gate without a policy)")
				}
			}
		case *ast.CallExpr:
			// (*outbound.Gate)(nil): a conversion of nil, a Gate that asks nobody.
			if par, ok := x.Fun.(*ast.ParenExpr); ok && len(x.Args) == 1 {
				if star, ok := par.X.(*ast.StarExpr); ok {
					if o, ok := isOutbound(star.X); ok && o.Sel.Name == "Gate" {
						out = append(out, fset.Position(x.Pos()).String()+": outbound.Gate nil Gate conversion")
					}
				}
			}
			// outbound.New(nil, ...): a Gate without a policy.
			if o, ok := isOutbound(x.Fun); ok && o.Sel.Name == "New" && len(x.Args) > 0 {
				if id, ok := x.Args[0].(*ast.Ident); ok && id.Name == "nil" {
					out = append(out, fset.Position(x.Pos()).String()+": outbound.New without a policy")
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
	"github.com/hejmo-home/hejmo/internal/outbound"
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
	_ = (*outbound.Gate)(nil).Client(outbound.ICS, 0)
	_ = &outbound.Gate{}
	_ = outbound.Gate{}
	_ = outbound.New(nil, nil)
	_ = outbound.AllowAll()
}`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "bad.go", bad, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := bypasses(fset, f, "internal/server")
	for _, want := range []string{"http.Get", "http.DefaultClient", "http.Client literal", "h2.Transport", "net.Dial", "net.Dialer", "net.LookupHost", "imports github.com/miekg/dns",
		"nil Gate", "Gate literal", "New without a policy", "outbound.AllowAll"} {
		if !slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, want) }) {
			t.Errorf("the check missed %q; found %v", want, got)
		}
	}
	// A client that is only passed around is fine.
	ok := `package x
import (
	"net/http"
	"github.com/hejmo-home/hejmo/internal/outbound"
)
type T struct{ c *http.Client; g *outbound.Gate }
func f(c *http.Client, g *outbound.Gate) *http.Request {
	_ = g.Client(outbound.ICS, 0)
	_ = outbound.New(outbound.Closed{}, nil)
	r, _ := http.NewRequest("GET", "/", nil)
	return r
}`
	f, _ = parser.ParseFile(fset, "ok.go", ok, 0)
	if got := bypasses(fset, f, "internal/server"); len(got) != 0 {
		t.Errorf("false alarm: %v", got)
	}
}
