package statusline

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The gateway's catalog retains the last good file and refreshes after 300s.
// Read it lazily once per render; rendering never performs a provider request.
const (
	gatewayCatalogTTL    = 300 * time.Second
	gatewayModelsPathEnv = "GPT_GATEWAY_CHATGPT_MODELS_PATH"
	catalogError         = "error"
)

type contextCatalog struct {
	once    sync.Once
	path    string
	now     time.Time
	windows map[string]int64
	state   string
	err     error
	warn    io.Writer
}

func newContextCatalog(home string, getenv func(string) string, now time.Time, warn io.Writer) *contextCatalog {
	path := getenv(gatewayModelsPathEnv)
	if path == "" {
		path = filepath.Join(home, ".local", "state", "gpt-gateway", "chatgpt-models.json")
	}
	if warn == nil {
		warn = os.Stderr
	}
	return &contextCatalog{path: path, now: now, warn: warn}
}

func canonicalContextModel(model string) string {
	return strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(model), "[1m]"), "-fast")
}

func (catalog *contextCatalog) capacity(model string) (int64, string) {
	catalog.once.Do(func() {
		defer func() {
			if catalog.err != nil && !errors.Is(catalog.err, os.ErrNotExist) {
				fmt.Fprintf(
					catalog.warn,
					"statusline: context catalog %s (%s): %v\n",
					catalog.path,
					catalog.state,
					catalog.err,
				)
			}
		}()
		// Reject special files without blocking on a FIFO before Stat.
		file, err := os.OpenFile(catalog.path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			catalog.err = err
			catalog.state = catalogError
			if errors.Is(err, os.ErrNotExist) {
				catalog.state = "missing"
			}
			return
		}
		defer func() {
			if err := file.Close(); err != nil {
				catalog.err = err
				catalog.state = catalogError
			}
		}()
		info, err := file.Stat()
		if err != nil {
			catalog.err = err
			catalog.state = catalogError
			return
		}
		if !info.Mode().IsRegular() {
			catalog.err = fmt.Errorf("catalog is not a regular file")
			catalog.state = catalogError
			return
		}
		var body struct {
			Models []struct {
				Slug   string `json:"slug"`
				Window *int64 `json:"context_window"`
			} `json:"models"`
		}
		decoder := json.NewDecoder(file)
		if err := decoder.Decode(&body); err != nil {
			catalog.err = err
			catalog.state = catalogError
			return
		}
		var trailing json.RawMessage
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			catalog.err = fmt.Errorf("trailing catalog content: %v", err)
			catalog.state = catalogError
			return
		}
		if len(body.Models) == 0 {
			catalog.err = fmt.Errorf("catalog has no models")
			catalog.state = catalogError
			return
		}
		catalog.windows = map[string]int64{}
		for _, m := range body.Models {
			if m.Slug == "" {
				catalog.err = fmt.Errorf("catalog model has no slug")
				catalog.state = catalogError
				return
			}
			if _, duplicate := catalog.windows[m.Slug]; duplicate || (m.Window != nil && *m.Window <= 0) {
				catalog.err = fmt.Errorf("catalog model %s has duplicate slug or invalid capacity", m.Slug)
				catalog.state = catalogError
				return
			}
			catalog.windows[m.Slug] = 0
			if m.Window != nil {
				catalog.windows[m.Slug] = *m.Window
			}
		}
		if catalog.now.Sub(info.ModTime()) > gatewayCatalogTTL {
			catalog.state = "stale"
		}
	})
	if catalog.state == "missing" || catalog.state == catalogError {
		return 0, catalog.state
	}
	window, ok := catalog.windows[canonicalContextModel(model)]
	if !ok || window <= 0 {
		return 0, "unknown"
	}
	return window, catalog.state
}

func (runtime Runtime) catalog() *contextCatalog {
	if runtime.contextCatalog != nil {
		return runtime.contextCatalog
	}
	return newContextCatalog(runtime.Home, runtime.getenv, runtime.now(), runtime.Warn)
}

func currentModel(requested, observed string) string {
	if requested == "" {
		return observed
	}
	// A previous model's response is historical after a switch, not the new model.
	if canonicalContextModel(requested) == canonicalContextModel(observed) {
		return observed
	}
	return requested
}
