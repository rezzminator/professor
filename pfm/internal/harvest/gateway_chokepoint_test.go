package harvest

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// gatewayExemptFiles are the ONLY files allowed to perform HTTP egress without
// going through the fetch gateway, each for a reason that cannot be designed
// away:
//
//   - gateway.go is the gateway.
//   - doh.go resolves DNS. The gateway's own dial needs DNS, so routing DNS
//     through the gateway is a resolution cycle, not a layering choice.
//   - net_chrome_transport.go follows redirects INSIDE the Chrome transport,
//     a layer beneath the gateway entirely.
//   - net_ua_transport.go hosts userAgentTransport, the http.RoundTripper
//     wrapper every gateway client's http.Client.Transport is set to. Its
//     RoundTrip method forwarding to the wrapped transport (t.base.RoundTrip)
//     is not a second application call bypassing the gateway — it is the
//     wire-send mechanism FOR a request gatewayAttempt already built and
//     validated, invoked by http.Client.Do itself. Same category as
//     net_chrome_transport.go, and invisible to the old substring matcher
//     because it never wrote ".Do(" or "http.NewRequest"; the type-aware
//     scanner correctly recognizes it as an http.RoundTripper.RoundTrip call
//     and this entry is its justification. It is its OWN file, not folded
//     into net.go, precisely so the exemption stays narrow: net.go is the
//     package's main network file, and a future bypass added there must
//     still be caught.
//   - find_works_sources.go hosts the per-source failure probe FindWorksReport
//     installs into the client it hands gatewayDo: an http.RoundTripper
//     wrapper whose RoundTrip forwards to its base transport to record a
//     provider's status, the same wire-send shape as net_ua_transport.go and
//     never an application call bypassing the gateway. It is its own file for
//     the same reason: find_works.go stays under the guard.
var gatewayExemptFiles = map[string]string{
	"gateway.go":              "is the gateway",
	"doh.go":                  "resolves DNS; routing it through the gateway would be a cycle",
	"net_chrome_transport.go": "is transport-internal, below the gateway",
	"net_ua_transport.go":     "is transport-internal: the User-Agent wrapper installed into every gateway client, forwarding to its base transport",
	"find_works_sources.go":   "is transport-internal: the per-source failure probe FindWorksReport installs into the client it hands the gateway, forwarding to its base transport",
}

// gatewayEgressFinding is one call site scanGatewayEgress judged as HTTP
// egress, tagged with WHICH shape matched it — the tag is what lets the
// meta-test below prove every enumerated shape is actually reachable, not
// just that the enumerator finds something.
type gatewayEgressFinding struct {
	shape string
	pos   token.Position
	text  string
}

// gatewayEgressClientMethods and gatewayEgressPackageFuncs are the exact
// shapes the gateway invariant enumerates. Both must be checked TYPE-AWARE:
// http.Header.Get and url.Values.Get share the method name "Get" with
// http.Client.Get but resolve to a different receiver type entirely, and only
// go/types can tell them apart from the syntax alone.
var (
	gatewayEgressClientMethods = map[string]bool{"Do": true, "Get": true, "Post": true, "Head": true, "PostForm": true}
	gatewayEgressPackageFuncs  = map[string]bool{
		"Get":                   true,
		"Post":                  true,
		"Head":                  true,
		"PostForm":              true,
		"NewRequest":            true,
		"NewRequestWithContext": true,
	}
)

// scanGatewayEgress type-checks files as one synthetic package and returns
// every call that issues, or could issue, outbound HTTP: a method
// Do/Get/Post/Head/PostForm on *net/http.Client, RoundTrip on any
// net/http.RoundTripper (interface or concrete transport), and the net/http
// package funcs Get/Post/Head/PostForm/NewRequest/NewRequestWithContext.
//
// A type-check failure is returned to the caller rather than swallowed: an
// importer error or an unparsed file must never silently report zero
// offenders, because that renders exactly like a clean scan.
func scanGatewayEgress(fset *token.FileSet, files []*ast.File) ([]gatewayEgressFinding, error) {
	conf := &types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	info := &types.Info{
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}
	if _, err := conf.Check("gatewayegressaudit", fset, files, info); err != nil {
		return nil, fmt.Errorf("type-check the package while scanning for HTTP egress: %w", err)
	}

	var findings []gatewayEgressFinding
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if selection, ok := info.Selections[sel]; ok {
				fn, ok := selection.Obj().(*types.Func)
				if !ok {
					return true
				}
				sig, ok := fn.Type().(*types.Signature)
				if !ok || sig.Recv() == nil {
					return true
				}
				recvType := sig.Recv().Type()
				if ptr, ok := recvType.(*types.Pointer); ok {
					recvType = ptr.Elem()
				}
				named, ok := recvType.(*types.Named)
				if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "net/http" {
					return true
				}
				switch {
				case named.Obj().Name() == "Client" && gatewayEgressClientMethods[fn.Name()]:
					findings = append(findings, gatewayEgressFinding{
						shape: "http.Client." + fn.Name(),
						pos:   fset.Position(call.Pos()),
						text:  types.ExprString(sel),
					})
				case fn.Name() == "RoundTrip":
					findings = append(findings, gatewayEgressFinding{
						shape: "http.RoundTripper.RoundTrip",
						pos:   fset.Position(call.Pos()),
						text:  types.ExprString(sel),
					})
				}
				return true
			}
			// Not a method value — check for a qualified package-level call
			// like http.Get(...). This is NOT a Selection in go/types terms,
			// so it is looked up separately through the package identifier.
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			pkgName, ok := info.Uses[ident].(*types.PkgName)
			if !ok || pkgName.Imported().Path() != "net/http" {
				return true
			}
			if gatewayEgressPackageFuncs[sel.Sel.Name] {
				findings = append(findings, gatewayEgressFinding{
					shape: "http." + sel.Sel.Name,
					pos:   fset.Position(call.Pos()),
					text:  types.ExprString(sel),
				})
			}
			return true
		})
	}
	return findings, nil
}

// TestEveryEgressGoesThroughTheGateway is a closed-world guard, not a spot
// check. The gateway only delivers its guarantees — one SSRF assertion, one
// cookie-jar rule, one redirect re-validation, one byte ceiling, one challenge
// ladder — if EVERY caller enters it. A single new HTTP call elsewhere
// silently reopens the exact split this package was refactored to close: the
// scholarly provider path could not pass a wall the generic ladder passed.
//
// Such a regression is invisible to every behavioural test, because the new
// call site works fine until the day it meets a wall. So the invariant is
// enforced against the SOURCE, TYPE-AWARE: this is not a substring match on
// ".Do(" — it type-checks the package and flags a call only when it actually
// resolves to net/http.Client's Do/Get/Post/Head/PostForm, an
// http.RoundTripper's RoundTrip, or a net/http package-level
// Get/Post/Head/PostForm/NewRequest/NewRequestWithContext. A lookalike like
// header.Get or url.Values.Get resolves to a different receiver type and is
// never flagged.
//
// WHAT THIS REPORTS WHEN IT IS ITSELF BROKEN: if it cannot read the package
// directory, finds no Go files to scan, or the package fails to type-check
// (an importer error, an unparsed file), it FAILS with that fact rather than
// passing on an empty enumeration — "we could not look" must never render as
// "there is nothing there".
func TestEveryEgressGoesThroughTheGateway(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("could not read the package directory to enumerate egress: %v", err)
	}
	var files []*ast.File
	scanned := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Clean(name)
		file, parseErr := parser.ParseFile(fset, path, nil, parser.AllErrors)
		if parseErr != nil {
			t.Fatalf("could not parse %s while enumerating egress: %v", name, parseErr)
		}
		files = append(files, file)
		scanned++
	}
	if scanned == 0 {
		t.Fatal("enumerated 0 Go files — the guard did not actually run, which is not the same as finding nothing")
	}

	findings, err := scanGatewayEgress(fset, files)
	if err != nil {
		t.Fatalf("could not look: %v", err)
	}

	var offenders []string
	for _, f := range findings {
		base := filepath.Base(f.pos.Filename)
		if _, exempt := gatewayExemptFiles[base]; exempt {
			continue
		}
		offenders = append(offenders, fmt.Sprintf("%s:%d: [%s] %s", base, f.pos.Line, f.shape, f.text))
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Fatalf(
			"HTTP egress outside the fetch gateway (%d site(s)); route these through retrieveGateway/gatewayAttempt, or justify an entry in gatewayExemptFiles:\n  %s",
			len(offenders),
			strings.Join(offenders, "\n  "),
		)
	}
	t.Logf("egress chokepoint holds: %d source file(s) scanned, %d exempt", scanned, len(gatewayExemptFiles))
}

// TestGatewayEgressEnumeratorCatchesEveryShape is the proof the enumerator
// above is not a partial list dressed up as a closed one: a fixture holding
// exactly ONE call of each enumerated shape must yield every shape as an
// offender when type-checked by the same scanGatewayEgress the guard runs.
// This is what the substring matcher this test replaced could not do — it
// matched only ".Do(" and "http.NewRequest", so a fixture call like
// client.Get(...), http.Head(...), or rt.RoundTrip(...) passed it silently.
func TestGatewayEgressEnumeratorCatchesEveryShape(t *testing.T) {
	const fixture = `package fixture

import "net/http"

func exercise() {
	var client *http.Client
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	_, _ = client.Do(req)
	_, _ = client.Get("http://example.com")
	_, _ = client.Post("http://example.com", "text/plain", nil)
	_, _ = client.Head("http://example.com")
	_, _ = client.PostForm("http://example.com", nil)
	_, _ = http.Get("http://example.com")
	_, _ = http.Post("http://example.com", "text/plain", nil)
	_, _ = http.Head("http://example.com")
	_, _ = http.PostForm("http://example.com", nil)
	_, _ = http.NewRequestWithContext(nil, "GET", "http://example.com", nil)
	var rt http.RoundTripper
	_, _ = rt.RoundTrip(req)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", fixture, 0)
	if err != nil {
		t.Fatalf("could not parse the egress-shape fixture: %v", err)
	}
	findings, err := scanGatewayEgress(fset, []*ast.File{file})
	if err != nil {
		t.Fatalf("could not type-check the egress-shape fixture: %v", err)
	}

	want := []string{
		"http.Client.Do", "http.Client.Get", "http.Client.Post", "http.Client.Head", "http.Client.PostForm",
		"http.RoundTripper.RoundTrip",
		"http.Get", "http.Post", "http.Head", "http.PostForm", "http.NewRequest", "http.NewRequestWithContext",
	}
	got := make(map[string]bool, len(findings))
	for _, f := range findings {
		got[f.shape] = true
	}
	var missed []string
	for _, shape := range want {
		if !got[shape] {
			missed = append(missed, shape)
		}
	}
	if len(missed) > 0 {
		t.Fatalf("enumerator missed shape(s) %v; findings = %#v", missed, findings)
	}
}

// TestGatewayEgressEnumeratorIgnoresLookalikeMethods pins the type-aware half
// of the invariant: a method literally named Get sits on http.Header and
// url.Values too, and a textual match would flag both as gateway bypasses.
// scanGatewayEgress must resolve the receiver type and flag neither.
func TestGatewayEgressEnumeratorIgnoresLookalikeMethods(t *testing.T) {
	const fixture = `package fixture

import (
	"net/http"
	"net/url"
)

func exercise(h http.Header, v url.Values) {
	_ = h.Get("X-Test")
	_ = v.Get("q")
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", fixture, 0)
	if err != nil {
		t.Fatalf("could not parse the lookalike fixture: %v", err)
	}
	findings, err := scanGatewayEgress(fset, []*ast.File{file})
	if err != nil {
		t.Fatalf("could not type-check the lookalike fixture: %v", err)
	}
	if len(findings) > 0 {
		t.Fatalf("lookalike Get methods were flagged as gateway egress: %#v", findings)
	}
}

// TestGatewayEgressEnumeratorFailsOnBrokenPackage: a package that cannot be
// type-checked must never report zero offenders — that is indistinguishable
// from a clean scan. scanGatewayEgress must return an error instead.
func TestGatewayEgressEnumeratorFailsOnBrokenPackage(t *testing.T) {
	const broken = `package fixture

func exercise() {
	undefinedThing.Do(nothing)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", broken, 0)
	if err != nil {
		t.Fatalf("could not parse the broken fixture: %v", err)
	}
	if _, err := scanGatewayEgress(fset, []*ast.File{file}); err == nil {
		t.Fatal("scanGatewayEgress error = nil for an unresolvable package, want a type-check failure")
	}
}
