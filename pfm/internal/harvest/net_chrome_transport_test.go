package harvest

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/fhttp/http2"
	"github.com/bogdanfinn/tls-client/profiles"
	utls "github.com/bogdanfinn/utls"
)

func TestChromeHeaderCaptureOracle(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.test/", nil)
	if err != nil {
		t.Fatal(err)
	}
	freq := chromeFRequest(req)
	if got := freq.Header[fhttp.HeaderOrderKey]; !equalStrings(got, chromeHeaderOrder) {
		t.Fatalf("header order=%v want %v", got, chromeHeaderOrder)
	}
	want := map[string]string{
		"Accept":                    chromeAccept,
		"Accept-Encoding":           chromeAcceptEncoding,
		"Accept-Language":           chromeAcceptLanguage,
		"Priority":                  chromePriority,
		"Sec-CH-UA":                 chromeSecCHUA,
		"Sec-CH-UA-Mobile":          chromeSecCHUAMobile,
		"Sec-CH-UA-Platform":        chromeSecCHUAPlatform,
		"Sec-Fetch-Dest":            "document",
		"Sec-Fetch-Mode":            "navigate",
		"Sec-Fetch-Site":            "none",
		"Sec-Fetch-User":            "?1",
		"Upgrade-Insecure-Requests": "1",
		"User-Agent":                chromeUA,
	}
	for key, value := range want {
		if got := freq.Header.Get(key); got != value {
			t.Errorf("header %s=%q want %q", key, got, value)
		}
	}
}

func TestChromeHTTP2CaptureOracle(t *testing.T) {
	p := profiles.Chrome_146
	settings := p.GetSettings()
	want := map[http2.SettingID]uint32{
		http2.SettingHeaderTableSize:   65536,
		http2.SettingEnablePush:        0,
		http2.SettingInitialWindowSize: 6291456,
		http2.SettingMaxHeaderListSize: 262144,
	}
	for id, value := range want {
		if got := settings[id]; got != value {
			t.Fatalf("HTTP/2 setting %d=%d want %d", id, got, value)
		}
	}
	if p.GetConnectionFlow() != 15663105 {
		t.Fatalf("HTTP/2 connection flow=%d want 15663105", p.GetConnectionFlow())
	}
	if got := strings.Join(p.GetPseudoHeaderOrder(), ","); got != ":method,:authority,:scheme,:path" {
		t.Fatalf("pseudo-header order=%q", got)
	}
}

func TestChromeAkamaiCaptureOracle(t *testing.T) {
	p := profiles.Chrome_146
	settings := p.GetSettings()
	akamai := fmt.Sprintf(
		"1:%d;2:%d;4:%d;6:%d|%d|0|m,a,s,p",
		settings[http2.SettingHeaderTableSize],
		settings[http2.SettingEnablePush],
		settings[http2.SettingInitialWindowSize],
		settings[http2.SettingMaxHeaderListSize],
		p.GetConnectionFlow(),
	)
	if got := fmt.Sprintf("%x", md5Bytes([]byte(akamai))); got != "52d84b11737d980aef856699f885ca86" {
		t.Fatalf("Akamai hash=%s text=%s", got, akamai)
	}
}

func TestChromeClientHelloCaptureOracle(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan []byte, 1)
	go func() {
		finish := func(payload []byte) {
			if err := server.Close(); err != nil {
				t.Errorf("close server: %v", err)
			}
			done <- payload
		}
		header := make([]byte, 5)
		if _, err := io.ReadFull(server, header); err != nil {
			finish(nil)
			return
		}
		body := make([]byte, int(binary.BigEndian.Uint16(header[3:])))
		if _, err := io.ReadFull(server, body); err != nil {
			finish(nil)
			return
		}
		finish(append(header, body...))
	}()
	conf := &utls.Config{ServerName: "example.test", NextProtos: []string{"h2", "http/1.1"}}
	uc := utls.UClient(client, conf, chrome146OracleProfile().GetClientHelloId(), false, false, true)
	_ = uc.Handshake()
	got := <-done
	if len(got) == 0 {
		t.Fatal("capture did not receive a ClientHello")
	}
	ja3, err := clientHelloJA3(got)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Chrome146 JA3=%s", ja3)
	t.Logf("Chrome146 JA3 hash=%x", md5Bytes([]byte(ja3)))
	ja3n := normalizeJA3(ja3)
	if got := fmt.Sprintf("%x", md5Bytes([]byte(ja3n))); got != "8e19337e7524d2573be54efb2b0784c9" {
		t.Fatalf("Chrome146 JA3N hash=%s text=%s", got, ja3n)
	}
	ja4, err := clientHelloJA4(got)
	if err != nil {
		t.Fatal(err)
	}
	if ja4 != "t13d1516h2_8daaf6152771_d8a2da3f94cd" {
		t.Fatalf("Chrome146 JA4=%s", ja4)
	}
}

func TestChromeTransportUsesPinnedResolver(t *testing.T) {
	target := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }),
	)
	defer target.Close()
	port := target.Listener.Addr().(*net.TCPAddr).Port
	host := fmt.Sprintf("public.example.test:%d", port)
	transport := newChromeTransport(func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	})
	if _, err := transport.RoundTrip(
		mustRequest("http://" + host),
	); err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "private") {
		t.Fatalf("private resolver answer accepted: %v", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustRequest(raw string) *http.Request {
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		panic(err)
	}
	return req
}

func clientHelloJA3(record []byte) (string, error) {
	if len(record) < 9 || record[0] != 22 || record[5] != 1 {
		return "", fmt.Errorf("invalid ClientHello record")
	}
	p := record[9:]
	if len(p) < 2+32+1 {
		return "", fmt.Errorf("short ClientHello")
	}
	version := binary.BigEndian.Uint16(p[:2])
	p = p[34:]
	if len(p) < 1 || int(p[0])+1 > len(p) {
		return "", fmt.Errorf("invalid session id")
	}
	p = p[1+int(p[0]):]
	if len(p) < 2 {
		return "", fmt.Errorf("missing ciphers")
	}
	cipherLen := int(binary.BigEndian.Uint16(p))
	p = p[2:]
	if cipherLen > len(p) || cipherLen%2 != 0 {
		return "", fmt.Errorf("invalid ciphers")
	}
	ciphers := make([]string, 0, cipherLen/2)
	for i := 0; i < cipherLen; i += 2 {
		id := binary.BigEndian.Uint16(p[i:])
		if !isGREASE(id) {
			ciphers = append(ciphers, fmt.Sprint(id))
		}
	}
	p = p[cipherLen:]
	if len(p) < 1 || int(p[0])+1 > len(p) {
		return "", fmt.Errorf("invalid compression")
	}
	p = p[1+int(p[0]):]
	if len(p) < 2 {
		return "", fmt.Errorf("missing extensions")
	}
	extLen := int(binary.BigEndian.Uint16(p))
	p = p[2:]
	if extLen > len(p) {
		return "", fmt.Errorf("invalid extensions")
	}
	ext := p[:extLen]
	exts, curves, points := []string{}, []string{}, []string{}
	for len(ext) > 0 {
		if len(ext) < 4 {
			return "", fmt.Errorf("short extension")
		}
		id := binary.BigEndian.Uint16(ext)
		l := int(binary.BigEndian.Uint16(ext[2:]))
		ext = ext[4:]
		if l > len(ext) {
			return "", fmt.Errorf("invalid extension length")
		}
		body := ext[:l]
		ext = ext[l:]
		if !isGREASE(id) {
			exts = append(exts, fmt.Sprint(id))
		}
		if id == 10 && len(body) >= 2 {
			n := int(binary.BigEndian.Uint16(body))
			for i := 2; i+1 < n+2 && i+1 < len(body); i += 2 {
				curve := binary.BigEndian.Uint16(body[i:])
				if !isGREASE(curve) {
					curves = append(curves, fmt.Sprint(curve))
				}
			}
		}
		if id == 11 && len(body) >= 1 {
			n := int(body[0])
			for i := 1; i < n+1 && i < len(body); i++ {
				points = append(points, fmt.Sprint(body[i]))
			}
		}
	}
	return fmt.Sprintf(
		"%d,%s,%s,%s,%s",
		version,
		strings.Join(ciphers, "-"),
		strings.Join(exts, "-"),
		strings.Join(curves, "-"),
		strings.Join(points, "-"),
	), nil
}

func isGREASE(v uint16) bool { return v&0x0f0f == 0x0a0a }

func md5Bytes(data []byte) [md5.Size]byte { return md5.Sum(data) }

func normalizeJA3(ja3 string) string {
	parts := strings.Split(ja3, ",")
	if len(parts) != 5 {
		return ja3
	}
	extensions := strings.Split(parts[2], "-")
	sort.Slice(extensions, func(i, j int) bool {
		var left, right int
		_, _ = fmt.Sscanf(extensions[i], "%d", &left)
		_, _ = fmt.Sscanf(extensions[j], "%d", &right)
		return left < right
	})
	parts[2] = strings.Join(extensions, "-")
	return strings.Join(parts, ",")
}

func clientHelloJA4(record []byte) (string, error) {
	if len(record) < 9 || record[0] != 22 || record[5] != 1 {
		return "", fmt.Errorf("invalid ClientHello record")
	}
	p := record[9:]
	if len(p) < 2+32+1 {
		return "", fmt.Errorf("short ClientHello")
	}
	legacyVersion := binary.BigEndian.Uint16(p[:2])
	p = p[34:]
	if len(p) < 1 || int(p[0])+1 > len(p) {
		return "", fmt.Errorf("invalid session id")
	}
	p = p[1+int(p[0]):]
	if len(p) < 2 {
		return "", fmt.Errorf("missing ciphers")
	}
	cipherLen := int(binary.BigEndian.Uint16(p))
	p = p[2:]
	if cipherLen > len(p) || cipherLen%2 != 0 {
		return "", fmt.Errorf("invalid ciphers")
	}
	ciphers := make([]string, 0, cipherLen/2)
	for i := 0; i < cipherLen; i += 2 {
		id := binary.BigEndian.Uint16(p[i:])
		if !isGREASE(id) {
			ciphers = append(ciphers, fmt.Sprintf("%04x", id))
		}
	}
	p = p[cipherLen:]
	if len(p) < 1 || int(p[0])+1 > len(p) {
		return "", fmt.Errorf("invalid compression")
	}
	p = p[1+int(p[0]):]
	if len(p) < 2 {
		return "", fmt.Errorf("missing extensions")
	}
	extLen := int(binary.BigEndian.Uint16(p))
	p = p[2:]
	if extLen > len(p) {
		return "", fmt.Errorf("invalid extensions")
	}
	ext := p[:extLen]
	version := legacyVersion
	sni := "i"
	alpn := "00"
	extensions := make([]string, 0, 16)
	signatures := make([]string, 0, 16)
	extensionCount := 0
	for len(ext) > 0 {
		if len(ext) < 4 {
			return "", fmt.Errorf("short extension")
		}
		id := binary.BigEndian.Uint16(ext)
		length := int(binary.BigEndian.Uint16(ext[2:]))
		ext = ext[4:]
		if length > len(ext) {
			return "", fmt.Errorf("invalid extension length")
		}
		body := ext[:length]
		ext = ext[length:]
		if isGREASE(id) {
			continue
		}
		extensionCount++
		if id != 0 && id != 16 {
			extensions = append(extensions, fmt.Sprintf("%04x", id))
		}
		switch id {
		case 0:
			sni = "d"
		case 13:
			if len(body) < 2 || int(binary.BigEndian.Uint16(body))+2 > len(body) {
				return "", fmt.Errorf("invalid signature algorithms")
			}
			for i := 2; i+1 < len(body); i += 2 {
				signatures = append(signatures, fmt.Sprintf("%04x", binary.BigEndian.Uint16(body[i:])))
			}
		case 16:
			if len(body) < 3 || int(binary.BigEndian.Uint16(body))+2 > len(body) || int(body[2])+3 > len(body) {
				return "", fmt.Errorf("invalid ALPN")
			}
			protocol := body[3 : 3+int(body[2])]
			if len(protocol) > 0 {
				alpn = string([]byte{protocol[0], protocol[len(protocol)-1]})
			}
		case 43:
			if len(body) < 1 || int(body[0])+1 > len(body) || int(body[0])%2 != 0 {
				return "", fmt.Errorf("invalid supported versions")
			}
			for i := 1; i+1 < 1+int(body[0]); i += 2 {
				candidate := binary.BigEndian.Uint16(body[i:])
				if !isGREASE(candidate) && candidate > version {
					version = candidate
				}
			}
		}
	}
	sort.Strings(ciphers)
	sort.Strings(extensions)
	versionText := ja4TLSVersion(version)
	first := fmt.Sprintf("t%s%s%02d%02d%s", versionText, sni, len(ciphers), extensionCount, alpn)
	second := sha256Prefix12(strings.Join(ciphers, ","))
	third := sha256Prefix12(strings.Join(extensions, ",") + "_" + strings.Join(signatures, ","))
	return first + "_" + second + "_" + third, nil
}

func sha256Prefix12(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum)[:12]
}

func ja4TLSVersion(version uint16) string {
	switch version {
	case 0x0304:
		return "13"
	case 0x0303:
		return "12"
	case 0x0302:
		return "11"
	case 0x0301:
		return "10"
	default:
		return fmt.Sprintf("%02x", version&0xff)
	}
}
