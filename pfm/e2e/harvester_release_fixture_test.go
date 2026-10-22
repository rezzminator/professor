//go:build e2e

package e2e

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const e2eHarvesterBinary = "#!/bin/sh\n" +
	"case \"${1-}\" in version) echo 0.1.0-fixture;; setup) echo 'harvester setup fixture';; " +
	"doctor) echo 'doctor: fixture healthy';; *) exit 2;; esac\n"

// Each isolated home gets its own trust file and loopback-only release proxy.
// The actual child CLI still fetches and verifies the canonical release URLs.
type harvesterReleaseFixture struct {
	proxyURL, caFile string
	certificate      tls.Certificate
	routes           map[string]string
	wg               sync.WaitGroup
	mu               sync.Mutex
	requests         []string
	errors           []error
}

func newHarvesterReleaseFixture(t *testing.T) *harvesterReleaseFixture {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Harvester release fixture"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"api.github.com", "github.com"}, IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(t.TempDir(), "harvester-release-ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	asset := "harvester_0.1.0-fixture_" + runtime.GOOS + "_" + runtime.GOARCH
	const base = "https://github.com/rezzminator/harvester/releases/download/v0.1.0-fixture/"
	fixture := &harvesterReleaseFixture{
		caFile: caFile, certificate: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key},
		routes: map[string]string{
			"https://api.github.com/repos/rezzminator/harvester/releases/latest": `{"tag_name":"v0.1.0-fixture"}`,
			base + "SHA256SUMS": fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte(e2eHarvesterBinary)), asset),
			base + asset:        e2eHarvesterBinary,
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.wg.Add(1)
		defer fixture.wg.Done()
		if err := fixture.serve(w, r); err != nil {
			fixture.mu.Lock()
			fixture.errors = append(fixture.errors, err)
			fixture.mu.Unlock()
		}
	}))
	fixture.proxyURL = server.URL
	t.Cleanup(func() {
		server.Close()
		fixture.wg.Wait()
		for _, err := range fixture.errors {
			t.Errorf("Harvester release fixture: %v", err)
		}
	})
	return fixture
}

func (f *harvesterReleaseFixture) serve(w http.ResponseWriter, r *http.Request) (result error) {
	if r.Method != http.MethodConnect || (r.Host != "api.github.com:443" && r.Host != "github.com:443") {
		http.Error(w, "release fixture refuses unlisted upstream", http.StatusBadGateway)
		return nil
	}
	connection, buffered, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return fmt.Errorf("accept release tunnel: %w", err)
	}
	defer func() { result = closeReleaseFixture(connection, result) }()
	if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return fmt.Errorf("bound release tunnel: %w", err)
	}
	if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return fmt.Errorf("acknowledge release tunnel: %w", err)
	}
	if err := buffered.Flush(); err != nil {
		return fmt.Errorf("flush release tunnel: %w", err)
	}
	secure := tls.Server(
		connection,
		&tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{f.certificate}},
	)
	request, err := http.ReadRequest(bufio.NewReader(secure))
	if err != nil {
		return fmt.Errorf("read release GET: %w", err)
	}
	defer func() { result = closeReleaseFixture(request.Body, result) }()
	host := strings.TrimSuffix(r.Host, ":443")
	address := "https://" + host + request.URL.RequestURI()
	content, allowed := f.routes[address]
	status := http.StatusOK
	if request.Method != http.MethodGet || (request.Host != host && request.Host != r.Host) || !allowed {
		status, content = http.StatusBadGateway, "release fixture refuses unlisted request"
	}
	f.mu.Lock()
	f.requests = append(f.requests, address)
	f.mu.Unlock()
	response := &http.Response{
		StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
		ProtoMajor: 1, ProtoMinor: 1, Header: make(http.Header), Close: true,
		Body: io.NopCloser(strings.NewReader(content)), ContentLength: int64(len(content)),
	}
	defer func() { result = closeReleaseFixture(response.Body, result) }()
	if err := response.Write(secure); err != nil {
		return fmt.Errorf("write release response for %s: %w", address, err)
	}
	return nil
}

func closeReleaseFixture(resource io.Closer, result error) error {
	if err := resource.Close(); err != nil {
		return errors.Join(result, fmt.Errorf("close release fixture resource: %w", err))
	}
	return result
}

func (f *harvesterReleaseFixture) assertDownloaded(t *testing.T) {
	t.Helper()
	asset := "harvester_0.1.0-fixture_" + runtime.GOOS + "_" + runtime.GOARCH
	const base = "https://github.com/rezzminator/harvester/releases/download/v0.1.0-fixture/"
	want := []string{
		"https://api.github.com/repos/rezzminator/harvester/releases/latest",
		base + "SHA256SUMS", base + asset,
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !reflect.DeepEqual(f.requests, want) {
		t.Fatalf("Harvester release requests=%v, want %v", f.requests, want)
	}
}

func TestHarvesterReleaseFixtureRefusesUnlistedUpstream(t *testing.T) {
	t.Parallel()
	fixture := newHarvesterReleaseFixture(t)
	proxy, err := url.Parse(fixture.proxyURL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
	defer client.CloseIdleConnections()
	response, err := client.Get("https://unlisted.invalid/harvester")
	if response != nil {
		if closeErr := response.Body.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	if err == nil || !strings.Contains(err.Error(), "Bad Gateway") {
		t.Fatalf("unlisted upstream response=%v err=%v, want fixture refusal without forwarding", response, err)
	}
}
