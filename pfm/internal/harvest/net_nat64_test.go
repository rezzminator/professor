package harvest

import (
	"net"
	"testing"
)

// TestPrivateIPJudgesNAT64And6to4ByEmbeddedIPv4 pins F28: privateIP had no
// NAT64/6to4 arm, so 64:ff9b::a9fe:a9fe (the metadata-service address
// 169.254.169.254 folded into the NAT64 well-known prefix) read as public.
// Table-driven per the brief's ask, covering the well-known /96 prefix, the
// documented local-use /48 prefix, 6to4, and the already-correct
// ::ffff:0:0/96 standard mapping (refuted as already handled — net.IP.To4()
// unwraps it, and every ip.IsXxx call privateIP makes does the same
// internally; kept here as a regression guard, not a new fix).
func TestPrivateIPJudgesNAT64And6to4ByEmbeddedIPv4(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		want bool
	}{
		{"NAT64 well-known prefix embeds a private link-local address", "64:ff9b::a9fe:a9fe", true},
		{"NAT64 well-known prefix embeds a public address", "64:ff9b::cb00:710a", false},
		{"NAT64 local-use /48 prefix embeds a private 10/8 address", "64:ff9b:1:a00:1:100::", true},
		{"NAT64 local-use /48 prefix embeds a public address", "64:ff9b:1:cb00:71:a00::", false},
		{"6to4 embeds a private 10/8 address", "2002:a00:101::", true},
		{"6to4 embeds a public address", "2002:cb00:710a::", false},
		{"standard IPv4-mapped ::ffff:0:0/96 embeds a private address", "::ffff:169.254.169.254", true},
		{"standard IPv4-mapped ::ffff:0:0/96 embeds a public address", "::ffff:203.0.113.10", false},
		{"an ordinary public IPv6 address stays public", "2001:db8::1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("net.ParseIP(%q) failed", tc.ip)
			}
			if got := privateIP(ip); got != tc.want {
				t.Fatalf("privateIP(%s) = %v, want %v", tc.ip, got, tc.want)
			}
		})
	}
}

func TestEmbeddedIPv4ReturnsNilForARealIPv4Address(t *testing.T) {
	if got := embeddedIPv4(net.ParseIP("203.0.113.10")); got != nil {
		t.Fatalf("embeddedIPv4(real IPv4) = %v, want nil", got)
	}
	if got := embeddedIPv4(net.ParseIP("::ffff:203.0.113.10")); got != nil {
		t.Fatalf("embeddedIPv4(standard-mapped) = %v, want nil (net.IP.To4() already unwraps it)", got)
	}
}
