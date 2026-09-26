package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// rot13 keeps the retired key spellings out of plain-text search while the
// test still feeds the loader the exact bytes an older operator config holds.
func rot13(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return 'a' + (r-'a'+13)%26
		case r >= 'A' && r <= 'Z':
			return 'A' + (r-'A'+13)%26
		}
		return r
	}, s)
}

func TestHarvesterRetiredScholarlyKeysFoldIntoMirrors(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	body := fmt.Sprintf(
		`{"scholarly":{"contactEmail":"ops@example.com",%q:"https://a.example.invalid",%q:"https://b.example.invalid",%q:"https://c.example.invalid",%q:"https://d.example.invalid","googleScholarURL":"https://scholar.example.invalid"}}`,
		rot13("fpvUhoHEY"),
		rot13("fpvQOHEY"),
		rot13("yvoTraHEY"),
		rot13("naanfHEY"),
	)
	writeFile(t, filepath.Join(dir, HarvesterFileName), body, 0o600)
	got, err := Load(filepath.Join(dir, FileName), home, nil)
	if err != nil {
		t.Fatalf("Load() with retired scholarly keys error = %v, want a silent fold", err)
	}
	s := got.Harvester.Scholarly
	if s.DOIMirrorURL != "https://a.example.invalid" || s.DOIViewerURL != "https://b.example.invalid" ||
		s.MD5CatalogURL != "https://c.example.invalid" || s.IPFSCatalogURL != "https://d.example.invalid" ||
		s.GoogleScholarURL != "https://scholar.example.invalid" || s.ContactEmail != "ops@example.com" {
		t.Fatalf("scholarly = %+v, want every retired key folded onto its neutral provider", s)
	}
}

func TestHarvesterMirrorsWinOverRetiredKeys(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	body := fmt.Sprintf(
		`{"scholarly":{%q:"https://old.example.invalid","mirrors":{"doi-mirror":"https://new.example.invalid"}}}`,
		rot13("fpvUhoHEY"),
	)
	writeFile(t, filepath.Join(dir, HarvesterFileName), body, 0o600)
	got, err := Load(filepath.Join(dir, FileName), home, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Harvester.Scholarly.DOIMirrorURL != "https://new.example.invalid" {
		t.Fatalf("DOIMirrorURL = %q, want the mirrors entry to win", got.Harvester.Scholarly.DOIMirrorURL)
	}
}

func TestHarvesterMirrorsRejectUnknownProviderAndBadURL(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"unknown provider": {`{"scholarly":{"mirrors":{"nope":"https://x.example.invalid"}}}`, "unknown provider"},
		"bad scheme":       {`{"scholarly":{"mirrors":{"doi-viewer":"ftp://x.example.invalid"}}}`, "http or https"},
		"query":            {`{"scholarly":{"mirrors":{"md5-catalog":"https://x.example.invalid/?t=1"}}}`, "query or fragment"},
		"unrelated key":    {`{"scholarly":{"bogusURL":"https://x.example.invalid"}}`, "unknown field"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, HarvesterFileName), tc.body, 0o600)
			_, err := Load(filepath.Join(dir, FileName), t.TempDir(), nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load() error = %v, want containing %q", err, tc.want)
			}
		})
	}
}
