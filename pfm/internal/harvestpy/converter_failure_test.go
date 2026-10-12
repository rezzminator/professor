package harvestpy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

// TestConverterFailureReachesTheCallerByName: the sidecar's own named failure
// (an ok:false answer, as converterFailure wraps it) is published as a
// conversion failure carrying the converter's words — never "could not
// classify", never the exception class or the stderr tail.
func TestConverterFailureReachesTheCallerByName(t *testing.T) {
	named := "the feed parsed to no title and no items: a broken or empty feed " +
		"(Couldn't find end of Start Tag chan line 1); a feedparser fallback on a broken feed is unmeasured"
	err := converterFailure("ValueError", "ValueError: "+named, "conversion failed for kind='feed': ValueError: "+named)
	source := "/tmp/demo/broken-feed.xml"
	published := harvest.PublicFailure(source, harvest.Result{Source: source, Kind: "feed", Error: err.Error()})
	if published.ErrorKind != "conversion" {
		t.Errorf("kind = %q, want conversion", published.ErrorKind)
	}
	if !strings.Contains(published.Error, named) {
		t.Errorf("text = %q, want the converter's named failure %q", published.Error, named)
	}
	for _, refuse := range []string{"could not classify", "ValueError", "stderr"} {
		if strings.Contains(published.Error, refuse) {
			t.Errorf("text = %q carries %q", published.Error, refuse)
		}
	}
}

func TestHTMLConversionLossesStayVisible(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("TOOLCHAIN-MISSING python3: %v", err)
	}
	root := t.TempDir()
	converter := filepath.Join(root, "converter.py")
	if err := os.WriteFile(converter, ConverterSource(), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(root, "probe.py")
	const script = `import contextlib, io, runpy, sys, types
ns = runpy.run_path(sys.argv[1])
case = sys.argv[2]
if case == 'island':
 islands = ns['_Islands']('original')
 islands.blocks = [(['| lost data |'], 'lost data')]
 output = ns['_restore_islands']('Article body', islands)
 assert '_Converter gap:' in output and 'omitted' in output, output
elif case == 'metadata':
 module = types.ModuleType('trafilatura.metadata')
 def broken(*args, **kwargs): raise RuntimeError('fixture metadata crash')
 module.extract_metadata = broken
 sys.modules['trafilatura'] = types.ModuleType('trafilatura')
 sys.modules['trafilatura.metadata'] = module
 stderr = io.StringIO()
 with contextlib.redirect_stderr(stderr): meta = ns['html_metadata']('<html>fixture</html>')
 assert 'metadata_error' in meta and 'RuntimeError' in meta['metadata_error'], meta
 assert 'RuntimeError' in stderr.getvalue(), stderr.getvalue()
 # The public protocol, not just a stderr log, must carry the gap.
 globals = ns['convert'].__globals__
 globals['_CONVERTERS']['txt'] = lambda path, request: (ns['html_metadata']('<html>fixture</html>') and 'body', [])
 output = ns['convert']({'path': sys.argv[1], 'kind': 'txt'})
 assert '_Converter gap:' in output['markdown'] and 'metadata' in output['markdown'], output
elif case == 'image':
 class Image:
  def get(self, key, default=None): return {'src':'data:image/png;base64,fixture','alt':'Gauge diagram'}.get(key,default)
 output = ns['_image_markdown'](Image())
 assert 'Converter gap:' in output and 'Gauge diagram' in output, output
elif case == 'errors':
 class Cell:
  tag = 'td'
  def get(self,key): return 'broken' if key == 'colspan' else None
  def iter(self,*args): return []
 class Row:
  def xpath(self,*args): return [Cell()]
  def getparent(self): return types.SimpleNamespace(tag='tbody')
 class Table:
  def xpath(self,*args): return [Row()]
  def find(self,*args): return None
 class Tree:
  def xpath(self,*args): return [types.SimpleNamespace(get=lambda key:'https://[broken')]
 globals = ns['_table_markdown'].__globals__
 globals['_cell_markdown'] = lambda cell: 'cell'
 stderr = io.StringIO()
 with contextlib.redirect_stderr(stderr):
  assert ns['_document_base'](Tree(),'https://example.test/') == 'https://example.test/'
  assert ns['_resolved']('https://example.test/','https://[broken') == 'https://[broken'
  assert 'cell' in ns['_table_markdown'](Table())
 output = stderr.getvalue()
 for context in ('base href','resolve link','colspan'):
  assert context in output and 'ValueError' in output, output
`
	if err := os.WriteFile(probe, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"island", "metadata", "image", "errors"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, python, "-I", probe, converter, name).CombinedOutput()
			if err != nil {
				t.Fatalf("HTML %s loss contract: %v\n%s", name, err, output)
			}
		})
	}
}
