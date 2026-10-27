package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"

	"github.com/BurntSushi/toml"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

const (
	extensionEnabledPlugins = "enabledPlugins"
	extensionPluginConfigs  = "pluginConfigs"
	extensionHookState      = "state"
	extensionEnabled        = "enabled"
	extensionOptions        = "options"
	extensionMarketplaces   = "marketplaces"
	extensionAdd            = "add"
	extensionPlugins        = "plugins"
	extensionHooks          = "hooks"
)

var (
	extensionClaude   = pfmengine.MustLookup(pfmengine.Claude).LongName
	extensionCodex    = pfmengine.MustLookup(pfmengine.Codex).LongName
	extensionOpenCode = pfmengine.MustLookup(pfmengine.OpenCode).LongName
)

const extensionOwnershipName = "extension-ownership.json"

type extensionValue struct {
	Path      []string `json:"path"`
	Before    any      `json:"before,omitempty"`
	HadBefore bool     `json:"hadBefore"`
	Last      any      `json:"last,omitempty"`
}

type extensionRecord struct {
	MarketplaceCaptured bool                      `json:"marketplaceCaptured,omitempty"`
	MarketplaceBefore   any                       `json:"marketplaceBefore,omitempty"`
	MarketplaceLast     any                       `json:"marketplaceLast,omitempty"`
	Name                string                    `json:"name"`
	Engine              string                    `json:"engine"`
	Target              string                    `json:"target"`
	Home                string                    `json:"home"`
	Values              map[string]extensionValue `json:"values"`
	PackageAdded        bool                      `json:"packageAdded,omitempty"`
	PackageBefore       bool                      `json:"packageBefore,omitempty"`
	PluginArrayBefore   bool                      `json:"pluginArrayBefore,omitempty"`
	CreatedTables       [][]string                `json:"createdTables,omitempty"`
	Env                 map[string]string         `json:"env,omitempty"`
	InstalledByPFM      bool                      `json:"installedByPFM,omitempty"`
	MarketplaceAdded    bool                      `json:"marketplaceAdded,omitempty"`
}

type extensionOwnership struct {
	Version int                        `json:"version"`
	Records map[string]extensionRecord `json:"records"`
}

// Keep one stable inode outside the root uninstall retires. The lock spans the
// entire applied install, including plugin CLI writes and ownership capture.
func lockInstaller(root string) (func() error, error) {
	path := filepath.Join(filepath.Dir(root), ".installer.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create installer lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open installer lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, fmt.Errorf("lock installer %s (another installer may be running; retry): %w", path,
			errors.Join(err, file.Close()))
	}
	return func() error {
		if err := errors.Join(syscall.Flock(int(file.Fd()), syscall.LOCK_UN), file.Close()); err != nil {
			return fmt.Errorf("release installer lock %s: %w", path, err)
		}
		return nil
	}, nil
}

func readExtensionOwnership(root string) (extensionOwnership, error) {
	result := extensionOwnership{Version: 1, Records: map[string]extensionRecord{}}
	path := filepath.Join(root, extensionOwnershipName)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("read extension ownership %s: %w", path, err)
	}
	if err := unmarshalKeepingNumbers(raw, &result); err != nil {
		return result, fmt.Errorf("decode extension ownership %s: %w", path, err)
	}
	if result.Version != 1 || result.Records == nil {
		return result, fmt.Errorf("invalid extension ownership %s", path)
	}
	return result, nil
}

func readExtensionDocument(path, engine string) ([]byte, map[string]any, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, map[string]any{}, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read extension %s config %s: %w", engine, path, err)
	}
	var document map[string]any
	switch engine {
	case extensionCodex:
		_, err = toml.Decode(string(raw), &document)
	case extensionOpenCode:
		document, err = decodeJSONCObject(raw)
	default:
		err = unmarshalKeepingNumbers(raw, &document)
	}
	if err != nil || document == nil {
		return nil, nil, fmt.Errorf("extension %s config %s unreadable: %v", engine, path, err)
	}
	return raw, document, nil
}

func extensionLookup(document map[string]any, path []string) (any, bool, error) {
	for i, key := range path {
		value, has := document[key]
		if i == len(path)-1 {
			return value, has, nil
		}
		if !has {
			return nil, false, nil
		}
		next, ok := value.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("extension field %s must be an object", strings.Join(path[:i+1], "."))
		}
		document = next
	}
	return nil, false, errors.New("empty extension config path")
}

func extensionSet(document map[string]any, path []string, value any, remove bool) error {
	for i, key := range path {
		if i == len(path)-1 {
			if remove {
				delete(document, key)
			} else {
				document[key] = value
			}
			return nil
		}
		current, has := document[key]
		if !has {
			if remove {
				return nil
			}
			current = map[string]any{}
			document[key] = current
		}
		next, ok := current.(map[string]any)
		if !ok {
			return fmt.Errorf("extension field %s must be an object", strings.Join(path[:i+1], "."))
		}
		document = next
	}
	return errors.New("empty extension config path")
}

// ownExtensionValue remembers only writes, never a matching foreign value.
func ownExtensionValue(document map[string]any, record *extensionRecord, path []string, want any) error {
	current, has, err := extensionLookup(document, path)
	if err != nil {
		return err
	}
	key := strings.Join(path, "/")
	owned, wasOwned := record.Values[key]
	if extensionEqual(current, want) && has {
		return nil
	}
	if !wasOwned {
		owned = extensionValue{Path: path, Before: current, HadBefore: has}
	}
	rememberExtensionTables(document, path, record)
	owned.Last = want
	record.Values[key] = owned
	return extensionSet(document, path, want, false)
}

func extensionEqual(a, b any) bool {
	// Ownership is persisted through JSON; compare its normalized representation.
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	return err == nil && bytes.Equal(left, right)
}

func restoreExtensionValues(document map[string]any, record extensionRecord) error {
	for _, value := range record.Values {
		current, has, err := extensionLookup(document, value.Path)
		if err != nil {
			return err
		}
		if has && extensionEqual(current, value.Last) {
			if err := extensionSet(document, value.Path, value.Before, !value.HadBefore); err != nil {
				return err
			}
		}
	}
	pruneExtensionTables(document, record.CreatedTables)
	return nil
}

// encodeExtensionEdit reuses the installer's surgical JSONC and TOML editors.
func encodeExtensionEdit(
	raw []byte,
	engine string,
	before, after map[string]any,
	record extensionRecord,
) ([]byte, error) {
	if engine == extensionClaude {
		return encodeSettingsDocument(after)
	}
	if engine == extensionOpenCode {
		if len(raw) == 0 {
			raw = []byte("{}\n")
		}
		if reflect.DeepEqual(before["plugin"], after["plugin"]) {
			return raw, nil
		}
		if _, has := after["plugin"]; !has {
			return removeJSONCProperty(raw, 0, "plugin")
		}
		value, err := json.Marshal(after["plugin"])
		if err != nil {
			return nil, fmt.Errorf("encode OpenCode extension plugins: %w", err)
		}
		return setJSONCProperty(raw, 0, "plugin", value)
	}
	updated := string(raw)
	for _, owned := range record.Values {
		current, has, err := extensionLookup(after, owned.Path)
		if err != nil {
			return nil, err
		}
		prior, existed, err := extensionLookup(before, owned.Path)
		if err != nil {
			return nil, err
		}
		if has == existed && extensionEqual(current, prior) {
			continue
		}
		var encoded string
		if has {
			encoded, err = encodeCodexValues(map[string]any{owned.Path[len(owned.Path)-1]: current})
			if err != nil {
				return nil, err
			}
		}
		start, end, found := codexDeclaration(updated, owned.Path, false)
		if found {
			if has {
				originalKey := extensionAssignmentIndex(updated[start:end])
				encodedKey := extensionAssignmentIndex(encoded)
				if originalKey < 0 || encodedKey < 0 {
					return nil, errors.New("extension TOML assignment has no value separator")
				}
				encoded = updated[start:start+originalKey+1] + encoded[encodedKey+1:]
			}
			updated = updated[:start] + encoded + updated[end:]
			continue
		}
		if !has {
			continue
		}
		table := owned.Path[:len(owned.Path)-1]
		if _, end, found := codexDeclaration(updated, table, true); found {
			updated = updated[:end] + encoded + updated[end:]
		} else {
			parts := make([]string, len(table))
			for i, key := range table {
				parts[i] = fmt.Sprintf("%q", key)
			}
			updated = strings.TrimRight(updated, "\n") + "\n\n[" + strings.Join(parts, ".") + "]\n" + encoded
		}
	}
	for _, table := range record.CreatedTables {
		value, has, err := extensionLookup(after, table)
		if err != nil {
			return nil, err
		}
		object, _ := value.(map[string]any)
		if !has || (object != nil && len(object) == 0) {
			if start, end, found := codexDeclaration(updated, table, true); found {
				updated = updated[:start] + updated[end:]
			}
		}
	}
	var checked map[string]any
	if _, err := toml.Decode(updated, &checked); err != nil {
		return nil, fmt.Errorf("validate extension TOML edit: %w", err)
	}
	if !extensionEqual(checked, after) {
		return nil, errors.New("extension TOML edit did not preserve the planned config")
	}
	return []byte(updated), nil
}

// Keep the complete original left-hand side, including dotted keys and quoted '='.
func extensionAssignmentIndex(raw string) int {
	var quote byte
	escaped := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if quote != 0 {
			switch {
			case escaped:
				escaped = false
			case quote == '"' && c == '\\':
				escaped = true
			case c == quote:
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '=':
			return i
		}
	}
	return -1
}

func (installer *engine) writeExtensionDocument(path string, raw, want []byte) error {
	if bytes.Equal(raw, want) {
		return nil
	}
	return installer.change("converge extensions in "+path, func() error {
		current, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) && raw == nil {
			err = nil
		}
		if err != nil {
			return fmt.Errorf("reread extension config %s: %w", path, err)
		}
		if !bytes.Equal(current, raw) {
			return fmt.Errorf("extension config %s changed while planning; rerun pfm install --yes", path)
		}
		return atomicfile.Write(physicalSettingsPath(path), want, 0o600)
	})
}

func rememberExtensionTables(document map[string]any, path []string, record *extensionRecord) {
	for i := 1; i < len(path); i++ {
		_, has, err := extensionLookup(document, path[:i])
		if err != nil || has {
			continue
		}
		found := false
		for _, old := range record.CreatedTables {
			if reflect.DeepEqual(old, path[:i]) {
				found = true
				break
			}
		}
		if !found {
			record.CreatedTables = append(record.CreatedTables, append([]string(nil), path[:i]...))
		}
	}
}

func pruneExtensionTables(document map[string]any, tables [][]string) {
	for i := len(tables) - 1; i >= 0; i-- {
		path := tables[i]
		value, has, err := extensionLookup(document, path)
		if err != nil || !has {
			continue
		}
		object, ok := value.(map[string]any)
		if ok && len(object) == 0 {
			_ = extensionSet(document, path, nil, true)
		}
	}
}
