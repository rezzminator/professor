package harvest

import "net"

// embeddedIPv4 extracts the IPv4 address folded into a NAT64 well-known
// prefix (64:ff9b::/96, RFC 6052), a NAT64 local-use prefix (64:ff9b:1::/48,
// RFC 6052 §2.2's /48 embedding, documented in RFC 8215), or a 6to4 address
// (2002::/16, RFC 3056). privateIP (net.go) calls this so a NAT64/6to4
// gateway's embedded private address is judged by what it actually reaches
// rather than by the wrapper's own (public) /96 or /16 prefix (F28). Returns
// nil for every other address shape, including a real IPv4 address or a
// standard-mapped ::ffff:0:0/96 one — ip.To4() already handles both, and
// every net.IP.IsXxx call privateIP makes first does the same internally.
func embeddedIPv4(ip net.IP) net.IP {
	if ip.To4() != nil {
		return nil
	}
	v6 := ip.To16()
	if v6 == nil {
		return nil
	}
	switch {
	case v6[0] == 0x00 && v6[1] == 0x64 && v6[2] == 0xff && v6[3] == 0x9b &&
		v6[4] == 0 && v6[5] == 0 && v6[6] == 0 && v6[7] == 0 &&
		v6[8] == 0 && v6[9] == 0 && v6[10] == 0 && v6[11] == 0:
		// The full 32-bit IPv4 address is the last 4 bytes verbatim.
		return net.IPv4(v6[12], v6[13], v6[14], v6[15])
	case v6[0] == 0x00 && v6[1] == 0x64 && v6[2] == 0xff && v6[3] == 0x9b && v6[4] == 0 && v6[5] == 1:
		// v4 bits 0-15 at bytes 6-7, a reserved zero octet at byte 8, v4
		// bits 16-31 at bytes 9-10.
		return net.IPv4(v6[6], v6[7], v6[9], v6[10])
	case v6[0] == 0x20 && v6[1] == 0x02:
		return net.IPv4(v6[2], v6[3], v6[4], v6[5])
	}
	return nil
}
