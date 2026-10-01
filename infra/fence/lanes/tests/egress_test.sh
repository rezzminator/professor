#!/usr/bin/env bash
# Packet fixtures are hex records from a tcpdump -i any capture in the fence.
set -uo pipefail

REPO="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../../.." && pwd -P)"
SUT="$REPO/infra/fence/egress.sh"
SHTEST_TAG=egress-test
# shellcheck source=/dev/null
source "$REPO/scripts/shtest.sh"

DNS_QUERY=0800000000000001030400060000000000000000450000393d5140004011ff567f0000017f00000b9227e9ed0025fe42791901000001000000000000076578616d706c6503636f6d0000010001
DNS_RESPONSE=08000000000000010304000600000000000000004500005955be40004011e6c97f00000b7f000001003592270045fe62791981800001000200000000076578616d706c6503636f6d0000010001c00c000100010000009d00046814179ac00c000100010000009d0004ac4293f3
SYN=080000000000000b0001040602e411f0a85800004500003c8dd840004006fecbac14000201010101be4401bb924e5e7e00000000a002faf0ae460000020405b40402080ad2dfd4c5000000000103030a
SYN_ACK=080000000000000b00010006c2911581f5c100004500003c211c00003f06ac8801010101ac14000201bbbe4410532503924e5e7fa012ff80559500000204ffd70402080aa1da73ced2dfd4c501030307
UDP=080000000000000b0001040602e411f0a8580000450000235e0e400040116ea4ac140002c0000201daef0009000f6e3866697874757265
OTHER_MARKER=080000000000000103040006000000000000000045000036bd27400040117f837f0000017f00000bd69300090022fe3f70666d2d6567726573732d656e64206f746865722d6e6f6e6365
END_MARKER=080000000000000103040006000000000000000045000038bd28400040117f807f0000017f00000bd69300090024fe4170666d2d6567726573732d656e6420666978747572652d6e6f6e6365
MARKER_REPLY=080000000000000103040006000000000000000045c00054760a0000400105d37f00000b7f0000010303f2600000000045000038bd28400040117f807f0000017f00000bd69300090024fe4170666d2d6567726573732d656e6420666978747572652d6e6f6e6365

pcap() {
  python3 - "$@" <<'PY'
import pathlib, struct, sys
path = pathlib.Path(sys.argv[1])
with path.open('wb') as f:
    f.write(bytes.fromhex('d4c3b2a10200040000000000000000000000040014010000'))
    for i, record in enumerate(sys.argv[2:]):
        packet = bytes.fromhex(record)
        f.write(struct.pack('<IIII', 1735689600 + i, 0, len(packet), len(packet)))
        f.write(packet)
PY
}

fixture() { mkdir -p "$T/$1"; pcap "$T/$1/fixture-nonce.pcap" "${@:2}" "$END_MARKER"; }
fixture clean
fixture dns "$DNS_QUERY" "$DNS_QUERY" "$DNS_RESPONSE"
fixture dest "$SYN" "$SYN_ACK" "$UDP"
fixture short "${DNS_QUERY:0:48}"
fixture marker "$MARKER_REPLY"
mkdir -p "$T/absent" "$T/absent-egress"
pcap "$T/absent/fixture-nonce.pcap" "$OTHER_MARKER"
pcap "$T/absent-egress/fixture-nonce.pcap" "$DNS_QUERY" "$OTHER_MARKER"
cp "$T/clean/fixture-nonce.pcap" "$T/wrong-link.pcap"
python3 - "$T/wrong-link.pcap" <<'PY'
import pathlib, sys
p = pathlib.Path(sys.argv[1])
b = bytearray(p.read_bytes())
b[20:24] = (1).to_bytes(4, 'little')
p.write_bytes(b)
PY
printf '0 packets dropped by kernel\n' >"$T/good.log"
: >"$T/no-summary.log"
printf '3 packets dropped by kernel\n' >"$T/dropped.log"
printf '1 packets captured\n16 packets received by filter\n0 packets dropped by kernel\n' >"$T/unread.log"

run_case() { OUT="$("$@" 2>&1)"; RC=$?; }
expect() {
  local label="$1" rc="$2" needle="$3"
  if [ "$RC" -eq "$rc" ] && [[ "$OUT" == *"$needle"* ]]; then ok "$label"
  else bad "$label" "rc=$RC expected=$rc" "wanted=$needle" "$OUT"; fi
}

run_case bash "$SUT" check "$T/clean/fixture-nonce.pcap" "$T/good.log"
expect 'clean capture' 0 'EGRESS PASS 0 DNS queries, 0 outside destinations — 0 packets recorded'
run_case bash "$SUT" check "$T/marker/fixture-nonce.pcap" "$T/good.log"
expect 'marker and loopback reply excluded' 0 'EGRESS PASS 0 DNS queries, 0 outside destinations — 0 packets recorded'
run_case bash "$SUT" check "$T/dns/fixture-nonce.pcap" "$T/good.log"
expect 'DNS queries counted by name' 1 'dns example.com ×2 first 00:00:00Z'
if [[ "$OUT" == *'EGRESS FAIL 2 DNS queries, 2 outside destinations'* ]]; then ok 'resolver counted despite loopback packet type'
else bad 'resolver counted despite loopback packet type' "$OUT"; fi
run_case bash "$SUT" check "$T/dest/fixture-nonce.pcap" "$T/good.log"
expect 'outgoing SYN and UDP counted' 1 'EGRESS FAIL 0 DNS queries, 2 outside destinations'
if [[ "$OUT" == *'1.1.1.1:443 ×1 first 00:00:00Z'* && "$OUT" == *'192.0.2.1:9 ×1 first 00:00:02Z'* && "$OUT" != *'172.20.0.2:48708'* ]]; then ok 'reply excluded from destinations'
else bad 'reply excluded from destinations' "$OUT"; fi
run_case bash "$SUT" check "$T/short/fixture-nonce.pcap" "$T/good.log"
expect 'truncated IP reported' 1 'undecoded ×1'
run_case env EGRESS_END_WAIT_S=0.2 bash "$SUT" check "$T/absent/fixture-nonce.pcap" "$T/good.log"
expect 'foreign marker does not complete capture' 2 'EGRESS NOT RECORDED — end marker not captured within 0.2 s'
run_case env EGRESS_END_WAIT_S=0.2 bash "$SUT" check "$T/absent-egress/fixture-nonce.pcap" "$T/good.log"
expect 'missing marker precedes egress fail' 2 'EGRESS NOT RECORDED — end marker not captured within 0.2 s'
run_case bash "$SUT" check "$T/missing.pcap" "$T/good.log"
expect 'missing pcap is not recorded' 2 'EGRESS NOT RECORDED — pcap'
: >"$T/empty.pcap"
run_case bash "$SUT" check "$T/empty.pcap" "$T/good.log"
expect 'empty pcap is not recorded' 2 'EGRESS NOT RECORDED — pcap'
run_case bash "$SUT" check "$T/wrong-link.pcap" "$T/good.log"
expect 'wrong linktype is not recorded' 2 'EGRESS NOT RECORDED — pcap'
run_case bash "$SUT" check "$T/clean/fixture-nonce.pcap" "$T/no-summary.log"
expect 'missing summary is not recorded' 2 'EGRESS NOT RECORDED — tcpdump exit summary missing'
run_case bash "$SUT" check "$T/clean/fixture-nonce.pcap" "$T/dropped.log"
expect 'kernel drops are not recorded' 2 'EGRESS NOT RECORDED — 3 packets dropped by kernel'
run_case bash "$SUT" check "$T/clean/fixture-nonce.pcap" "$T/unread.log"
expect 'received above captured uses complete pcap' 0 'EGRESS PASS 0 DNS queries, 0 outside destinations — 0 packets recorded'

cat >"$T/tcpdump-stub" <<'STUB'
#!/usr/bin/env bash
pcap=''
while [ "$#" -gt 0 ]; do
  if [ "$1" = -w ]; then pcap="$2"; shift 2; else shift; fi
done
case "$STUB_MODE" in
  silent) sleep 1; exit 0 ;;
  no-listen) echo 'recorder unavailable' >&2; exit 1 ;;
esac
echo 'tcpdump: listening on any, link-type LINUX_SLL2' >&2
if [ "$STUB_MODE" != missing ]; then
  cp "$STUB_PCAP" "$pcap"
  if [ "$STUB_MODE" != no-marker ]; then
    python3 - "$pcap" "$STUB_MARKER" <<'PY'
import pathlib, struct, sys
path = pathlib.Path(sys.argv[1])
packet = bytearray.fromhex(sys.argv[2])
payload = b'pfm-egress-end ' + path.stem.encode('ascii')
packet[22:24] = struct.pack('!H', 20 + 8 + len(payload))
packet[44:46] = struct.pack('!H', 8 + len(payload))
packet[48:] = payload
with path.open('ab') as f:
    f.write(struct.pack('<IIII', 1735689600, 0, len(packet), len(packet)))
    f.write(packet)
PY
  fi
fi
case "$STUB_MODE" in
  no-summary) ;;
  dropped) echo '2 packets dropped by kernel' >&2 ;;
  *) echo '0 packets dropped by kernel' >&2 ;;
esac
STUB
chmod +x "$T/tcpdump-stub"
export STUB_PCAP="$T/absent/fixture-nonce.pcap" STUB_MARKER="$END_MARKER" EGRESS_TCPDUMP="$T/tcpdump-stub"
run_case env EGRESS_TCPDUMP="$T/no-tcpdump" bash "$SUT" run bash -c 'printf command-output'
expect 'missing tcpdump still runs command' 2 'EGRESS NOT RECORDED — tcpdump not found'
if [[ "$OUT" == command-output* ]]; then ok 'unrecorded command output kept'; else bad 'unrecorded command output kept' "$OUT"; fi
for mode in no-listen silent missing no-summary dropped; do
  run_case env STUB_MODE="$mode" EGRESS_LISTEN_WAIT_S=0.2 EGRESS_END_WAIT_S=0.2 bash "$SUT" run bash -c 'printf command-output'
  case "$mode" in
    no-listen|silent) want='EGRESS NOT RECORDED — tcpdump not listening' ;;
    missing) want='EGRESS NOT RECORDED — pcap' ;;
    no-summary) want='EGRESS NOT RECORDED — tcpdump exit summary missing' ;;
    dropped) want='EGRESS NOT RECORDED — 2 packets dropped by kernel' ;;
  esac
  expect "recorder $mode" 2 "$want"
done
run_case env STUB_MODE=no-marker EGRESS_END_WAIT_S=0.2 bash "$SUT" run true
expect 'recorder exits before marker arrives' 2 'EGRESS NOT RECORDED — end marker not captured within 0.2 s'
run_case env STUB_MODE=good EGRESS_END_WAIT_S=0.2 bash "$SUT" run bash -c 'printf command-output; exit 7'
expect 'command rc retained with verdict' 7 'EGRESS PASS 0 DNS queries, 0 outside destinations'
if [[ "$OUT" == command-output* ]]; then ok 'recorded command output kept'; else bad 'recorded command output kept' "$OUT"; fi

shtest_end
