package harvest

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// driveVirusScanPage is Drive's large-file warning as drive.usercontent.google.com
// served it signed out for a public file (gdown's sample model), its inline
// styles and scripts trimmed.
const driveVirusScanPage = `<!DOCTYPE html><html><head><title>Google Drive - Virus scan warning</title></head>` +
	`<body><div class="uc-main"><div id="uc-dl-icon" class="image-container"><div class="drive-sprite-aux-download-file"></div></div>` +
	`<div id="uc-text"><p class="uc-warning-caption">Google Drive can't scan this file for viruses.</p>` +
	`<p class="uc-warning-subcaption"><span class="uc-name-size"><a href="/open?id=1l_5RK28JRL19wpT22B-DY9We3TVXnnQQ">fcn8s_from_caffe.npz</a> (476M)</span>` +
	` is too large for Google to scan for viruses. Would you still like to download this file?</p>` +
	`<form id="download-form" action="https://drive.usercontent.google.com/download" method="get">` +
	`<input type="submit" id="uc-download-link" class="goog-inline-block jfk-button jfk-button-action" value="Download anyway"/>` +
	`<input type="hidden" name="id" value="1l_5RK28JRL19wpT22B-DY9We3TVXnnQQ"><input type="hidden" name="export" value="download">` +
	`<input type="hidden" name="confirm" value="t"><input type="hidden" name="uuid" value="0731bdf8-42b1-43d4-a69d-b05bc8c9b37a"></form>` +
	`</div></div><div class="uc-footer"><hr class="uc-footer-divider"></div></body></html>`

func TestShareDirectLinkRewritesEachService(t *testing.T) {
	tests := []struct {
		source, service, target string
	}{
		{
			"https://www.dropbox.com/s/abc123xyz/report.pdf?dl=0",
			"dropbox",
			"https://www.dropbox.com/s/abc123xyz/report.pdf?dl=1",
		},
		{
			"https://dropbox.com/s/abc123xyz/report.pdf",
			"dropbox",
			"https://www.dropbox.com/s/abc123xyz/report.pdf?dl=1",
		},
		{
			"https://www.dropbox.com/scl/fi/k2x9q/data.csv?rlkey=r4nd0mkey&dl=0",
			"dropbox",
			"https://www.dropbox.com/scl/fi/k2x9q/data.csv?dl=1&rlkey=r4nd0mkey",
		},
		{"https://app.box.com/s/a1b2c3d4e5f6g7h8", "box", "https://app.box.com/shared/static/a1b2c3d4e5f6g7h8"},
		{
			"https://example.app.box.com/s/a1b2c3d4e5f6g7h8",
			"box",
			"https://example.app.box.com/shared/static/a1b2c3d4e5f6g7h8",
		},
		{
			"https://docs.google.com/document/d/195j9eDD3ccgjQRttHhJPymLJUCOUjs-jmwTrekvdjFE/edit?usp=sharing",
			"google-docs",
			"https://docs.google.com/document/d/195j9eDD3ccgjQRttHhJPymLJUCOUjs-jmwTrekvdjFE/export?format=docx",
		},
		{
			"https://docs.google.com/spreadsheets/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms/edit#gid=0",
			"google-sheets",
			"https://docs.google.com/spreadsheets/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms/export?format=xlsx",
		},
		{
			"https://docs.google.com/presentation/d/1EAYk18WDjIG-zp_0vLm3CsfQh_i8eXc67Jo2O9C6Vuc/edit",
			"google-slides",
			"https://docs.google.com/presentation/d/1EAYk18WDjIG-zp_0vLm3CsfQh_i8eXc67Jo2O9C6Vuc/export/pptx",
		},
		{
			"https://drive.google.com/file/d/1UEfsp7vKFqBb8C7Th8CuM2k0CxyKe2fy/view?usp=sharing",
			"google-drive",
			"https://drive.usercontent.google.com/download?confirm=t&export=download&id=1UEfsp7vKFqBb8C7Th8CuM2k0CxyKe2fy",
		},
		// Not share files: a published doc is already a public page, a folder
		// and a lookalike host are left alone.
		{"https://docs.google.com/document/d/e/2PACX-1vExample/pub", "", ""},
		{"https://drive.google.com/drive/folders/1UEfsp7vKFqBb8C7Th8CuM2k0CxyKe2fy", "", ""},
		{"https://www.dropbox.com.evil.test/s/abc123xyz/report.pdf", "", ""},
		{"https://www.dropbox.com/home", "", ""},
	}
	for _, test := range tests {
		t.Run(test.source, func(t *testing.T) {
			link, ok := shareDirectLink(test.source)
			if test.service == "" {
				if ok {
					t.Fatalf("shareDirectLink() = %#v; want no rewrite", link)
				}
				return
			}
			if !ok || link.service != test.service || link.target != test.target {
				t.Fatalf("shareDirectLink() = %#v, %v; want %s → %s", link, ok, test.service, test.target)
			}
		})
	}
}

// failOnRequest is a transport under which any request fails the test.
func failOnRequest(t *testing.T) *recordingTransport {
	return &recordingTransport{respond: func(request *http.Request) (*http.Response, error) {
		t.Errorf("a refused share link was fetched: %s", request.URL)
		return response(request, http.StatusOK, "text/html", "<p>sign in</p>"), nil
	}}
}

func TestShareRefusedServicesFailByNameWithoutAFetch(t *testing.T) {
	tests := []struct{ source, service string }{
		{"https://contoso.sharepoint.com/:w:/s/team/EXampleShareToken", "SharePoint"},
		{"https://contoso-my.sharepoint.com/:b:/p/someone/EXampleShareToken", "OneDrive for Business"},
		{"https://1drv.ms/b/s!AExampleShareToken", "OneDrive"},
		{"https://onedrive.live.com/?cid=EXAMPLE&id=EXAMPLE%21101", "OneDrive"},
		{"https://www.icloud.com/iclouddrive/0exampleShareToken#report", "iCloud"},
		{"https://share.icloud.com/photos/0exampleShareToken", "iCloud"},
		{"https://mega.nz/file/AbCdEfGh#ExampleKey", "MEGA"},
		{"https://wetransfer.com/downloads/0example/0example", "WeTransfer"},
		{"https://we.tl/t-ExampleToken", "WeTransfer"},
	}
	for _, test := range tests {
		t.Run(test.source, func(t *testing.T) {
			transport := failOnRequest(t)
			h := mustNew(t, Options{
				CacheDir:  t.TempDir(),
				Client:    &http.Client{Transport: transport},
				Chrome:    &http.Client{Transport: transport},
				Jina:      &http.Client{Transport: transport},
				Converter: &fakeConverter{},
			})
			for name, result := range map[string]Result{
				"read":          h.Fetch(context.Background(), test.source),
				"download_file": h.Download(context.Background(), test.source),
			} {
				want := "is a " + test.service + " link: a share link this harvester cannot open without signing in — download it yourself and read its path with harvester_read (files)"
				if !strings.Contains(result.Error, want) || result.ErrorKind != errorKindLogin || result.Path != "" {
					t.Fatalf("%s result = %#v; want the named %s refusal", name, result, test.service)
				}
			}
		})
	}
}

func TestShareRewriteReachesReadPageAndDownload(t *testing.T) {
	const source = "https://www.dropbox.com/s/abc123xyz/report.pdf?dl=0"
	transport := &recordingTransport{respond: func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("dl") == "1" {
			return response(request, http.StatusOK, "application/pdf", "%PDF-1.7\nreport\n%%EOF"), nil
		}
		return response(request, http.StatusOK, "text/html", strings.Repeat("<p>Dropbox preview</p>", 40)), nil
	}}
	h := mustNew(t, Options{
		CacheDir:  t.TempDir(),
		Client:    &http.Client{Transport: transport},
		Chrome:    &http.Client{Transport: transport},
		Converter: &fakeConverter{},
	})
	page := h.Fetch(context.Background(), source)
	if page.Error != "" || page.Kind != "pdf" || page.Method != "dropbox-download" || page.Source != source {
		t.Fatalf("read = %#v; want the PDF through dropbox-download", page)
	}
	file := h.Download(context.Background(), source)
	if file.Error != "" || file.Kind != "pdf" || file.Path == "" || file.Source != source {
		t.Fatalf("download = %#v; want the PDF file", file)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	for _, seen := range transport.seen {
		if !strings.Contains(seen, "dl=1") {
			t.Fatalf("requests = %#v; every fetch must be the direct dl=1 form", transport.seen)
		}
	}
}

func TestShareSignInPageIsANamedFailureNeverTheDocument(t *testing.T) {
	const source = "https://docs.google.com/document/d/1PrivateDocExample0000/edit"
	transport := &recordingTransport{respond: func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusOK, "text/html; charset=utf-8",
			"<html><head><title>Google Docs: Sign-in</title></head><body>"+
				strings.Repeat(
					"<p>Sign in to continue to Docs. Use your Google Account.</p>",
					30,
				)+"</body></html>"), nil
	}}
	h := mustNew(t, Options{
		CacheDir:  t.TempDir(),
		Client:    &http.Client{Transport: transport},
		Chrome:    &http.Client{Transport: transport},
		Jina:      &http.Client{Transport: transport},
		Converter: &fakeConverter{},
	})
	for name, result := range map[string]Result{
		"read":          h.Fetch(context.Background(), source),
		"download_file": h.Download(context.Background(), source),
	} {
		if !strings.Contains(result.Error, "Google Docs file") ||
			!strings.Contains(result.Error, "sign-in or interstitial page") ||
			result.ErrorKind != errorKindLogin ||
			result.Content != "" ||
			result.Path != "" {
			t.Fatalf("%s result = %#v; want the named sign-in failure, nothing stored", name, result)
		}
	}
}

func TestShareDriveVirusScanPageIsFollowedToTheFile(t *testing.T) {
	confirmed, ok := driveConfirmURL([]byte(driveVirusScanPage))
	parsed, err := url.Parse(confirmed)
	if !ok || err != nil || parsed.Hostname() != "drive.usercontent.google.com" ||
		parsed.Query().Get("uuid") != "0731bdf8-42b1-43d4-a69d-b05bc8c9b37a" ||
		parsed.Query().Get("id") != "1l_5RK28JRL19wpT22B-DY9We3TVXnnQQ" || parsed.Query().Get("confirm") != "t" {
		t.Fatalf("driveConfirmURL() = %q, %v; want the confirmed download with its uuid", confirmed, ok)
	}
	const source = "https://drive.google.com/file/d/1l_5RK28JRL19wpT22B-DY9We3TVXnnQQ/view?usp=sharing"
	transport := &recordingTransport{respond: func(request *http.Request) (*http.Response, error) {
		if request.URL.Hostname() == "drive.usercontent.google.com" && request.URL.Query().Get("uuid") != "" {
			return response(request, http.StatusOK, "application/pdf", "%PDF-1.7\nthe large file\n%%EOF"), nil
		}
		return response(request, http.StatusOK, "text/html; charset=utf-8", driveVirusScanPage), nil
	}}
	h := mustNew(t, Options{
		CacheDir:  t.TempDir(),
		Client:    &http.Client{Transport: transport},
		Chrome:    &http.Client{Transport: transport},
		Converter: &fakeConverter{},
	})
	page := h.Fetch(context.Background(), source)
	if page.Error != "" || page.Kind != "pdf" || page.Method != "google-drive-download" {
		t.Fatalf("read = %#v; want the file past the virus-scan page", page)
	}
	file := h.Download(context.Background(), source)
	if file.Error != "" || file.Kind != "pdf" || file.Path == "" {
		t.Fatalf("download = %#v; want the file past the virus-scan page", file)
	}
}
