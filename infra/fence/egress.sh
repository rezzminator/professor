#!/usr/bin/env bash
set -uo pipefail

not_recorded() { printf 'EGRESS NOT RECORDED — %s\n' "$1"; return 2; }

check() {
  local pcap="$1" log="$2"
  if ! command -v python3 >/dev/null 2>&1; then
    not_recorded 'python3 not found'
    return 2
  fi
  python3 - "$pcap" "$log" <<'PY'
import collections
import datetime
import ipaddress
import os
import pathlib
import re
import struct
import sys

pcap = pathlib.Path(sys.argv[1])
log = pathlib.Path(sys.argv[2])


def unavailable(reason):
    print(f'EGRESS NOT RECORDED — {reason}')
    return 2


if not pcap.is_file() or pcap.stat().st_size == 0:
    sys.exit(unavailable(f'pcap {pcap} missing'))
try:
    data = pcap.read_bytes()
except OSError as error:
    sys.exit(unavailable(f'pcap {pcap} unreadable: {error}'))
if len(data) < 24:
    sys.exit(unavailable(f'pcap {pcap} unreadable: short global header'))
magic = data[:4]
formats = {
    b'\xd4\xc3\xb2\xa1': ('<', 1000000),
    b'\xa1\xb2\xc3\xd4': ('>', 1000000),
    b'\x4d\x3c\xb2\xa1': ('<', 1000000000),
    b'\xa1\xb2\x3c\x4d': ('>', 1000000000),
}
if magic not in formats:
    sys.exit(unavailable(f'pcap {pcap} unreadable: not classic pcap'))
endian, precision = formats[magic]
linktype = struct.unpack_from(endian + 'I', data, 20)[0]
if linktype != 276:
    sys.exit(unavailable(f'pcap {pcap} unreadable: linktype {linktype}, expected 276'))
try:
    stderr = log.read_text(errors='replace')
except OSError as error:
    sys.exit(unavailable(f'tcpdump exit summary missing: {error}'))
drops = re.findall(r'(\d+) packets dropped by kernel', stderr)
if not drops:
    sys.exit(unavailable('tcpdump exit summary missing'))
if int(drops[-1]):
    sys.exit(unavailable(f'{drops[-1]} packets dropped by kernel'))
def name_from_dns(message):
    labels = []
    pos = 12
    for _ in range(128):
        if pos >= len(message):
            return '<invalid>'
        length = message[pos]
        pos += 1
        if length == 0:
            return '.'.join(labels) or '.'
        if length & 0xc0 or length > 63 or pos + length > len(message):
            return '<invalid>'
        labels.append(message[pos:pos + length].decode('ascii', 'replace'))
        pos += length
    return '<invalid>'


def first_time(seconds, fraction):
    return datetime.datetime.fromtimestamp(seconds + fraction / precision,
                                           datetime.timezone.utc).strftime('%H:%M:%SZ')


names = collections.Counter()
destinations = collections.Counter()
name_first = {}
destination_first = {}
undecoded = 0
packets = 0
marker_seen = False
marker = b'pfm-egress-end ' + pcap.stem.encode('ascii')
offset = 24
while offset < len(data):
    if offset + 16 > len(data):
        sys.exit(unavailable(f'pcap {pcap} unreadable: short packet header'))
    seconds, fraction, captured, original = struct.unpack_from(endian + 'IIII', data, offset)
    offset += 16
    if offset + captured > len(data):
        sys.exit(unavailable(f'pcap {pcap} unreadable: short packet record'))
    packet = data[offset:offset + captured]
    offset += captured
    packets += 1
    if len(packet) < 20:
        undecoded += 1
        continue
    protocol = int.from_bytes(packet[:2], 'big')
    packet_type = packet[10]
    ip = packet[20:]
    if protocol == 0x0800:
        if len(ip) < 20 or ip[0] >> 4 != 4:
            undecoded += 1
            continue
        header_length = (ip[0] & 15) * 4
        total_length = int.from_bytes(ip[2:4], 'big')
        if header_length < 20 or total_length < header_length or len(ip) < total_length:
            undecoded += 1
            continue
        if int.from_bytes(ip[6:8], 'big') & 0x1fff:
            continue
        transport = ip[header_length:total_length]
        next_header = ip[9]
        destination = ipaddress.IPv4Address(ip[16:20])
        outside = destination == ipaddress.IPv4Address('127.0.0.11') or destination not in ipaddress.IPv4Network('127.0.0.0/8')
        resolver = destination == ipaddress.IPv4Address('127.0.0.11')
    elif protocol == 0x86dd:
        if len(ip) < 40 or ip[0] >> 4 != 6:
            undecoded += 1
            continue
        payload_length = int.from_bytes(ip[4:6], 'big')
        if len(ip) < 40 + payload_length:
            undecoded += 1
            continue
        transport = ip[40:40 + payload_length]
        next_header = ip[6]
        destination = ipaddress.IPv6Address(ip[24:40])
        outside = destination != ipaddress.IPv6Address('::1')
        resolver = False
    else:
        continue
    if next_header == 1:
        packets -= 1
        continue
    if next_header not in (6, 17):
        continue
    if len(transport) < (20 if next_header == 6 else 8):
        undecoded += 1
        continue
    port = int.from_bytes(transport[2:4], 'big')
    when = first_time(seconds, fraction)
    if next_header == 17:
        udp_length = int.from_bytes(transport[4:6], 'big')
        if udp_length < 8 or len(transport) < udp_length:
            undecoded += 1
            continue
        payload = transport[8:udp_length]
        if protocol == 0x0800 and destination == ipaddress.IPv4Address('127.0.0.11') and port == 9 and payload.startswith(b'pfm-egress-end '):
            packets -= 1
            if payload == marker:
                marker_seen = True
            continue
        if protocol == 0x0800 and (port == 53 or resolver) and len(payload) >= 12 and not payload[2] & 0x80:
            name = name_from_dns(payload)
            names[name] += 1
            name_first.setdefault(name, when)
        outgoing = packet_type == 4 or resolver
    else:
        flags = transport[13]
        outgoing = bool(flags & 2) and not bool(flags & 16)
    if outside and outgoing:
        host = f'[{destination}]' if protocol == 0x86dd else str(destination)
        target = f'{host}:{port}'
        destinations[target] += 1
        destination_first.setdefault(target, when)

queries = sum(names.values())
outside_count = sum(destinations.values())
if not marker_seen:
    sys.exit(unavailable(f'end marker not captured within {os.environ.get("EGRESS_END_WAIT_S", "5")} s'))
if queries or outside_count or undecoded:
    print(f'EGRESS FAIL {queries} DNS queries, {outside_count} outside destinations ({pcap})')
    for name in sorted(names, key=lambda key: (name_first[key], key)):
        print(f'  dns {name} ×{names[name]} first {name_first[name]}')
    for target in sorted(destinations, key=lambda key: (destination_first[key], key)):
        print(f'  {target} ×{destinations[target]} first {destination_first[target]}')
    if undecoded:
        print(f'  undecoded ×{undecoded}')
    sys.exit(1)
print(f'EGRESS PASS 0 DNS queries, 0 outside destinations — {packets} packets recorded ({pcap})')
PY
}

run() {
  local recorder="${EGRESS_TCPDUMP:-tcpdump}" stamp dir pcap log filter pid rc i limit ready reason verdict end_limit
  dir="${PFM_TEST_TIMING_DIR:-/tmp}/egress"
  stamp="$(date -u +%Y%m%dT%H%M%S)-$$-$RANDOM"
  pcap="$dir/$stamp.pcap"
  log="$dir/$stamp.log"
  if ! command -v "$recorder" >/dev/null 2>&1; then
    "$@"; rc=$?
    not_recorded 'tcpdump not found' || :
    if [ "$rc" -ne 0 ]; then return "$rc"; fi
    return 2
  fi
  if ! mkdir -p "$dir"; then
    "$@"; rc=$?
    not_recorded "pcap $pcap missing" || :
    if [ "$rc" -ne 0 ]; then return "$rc"; fi
    return 2
  fi
  filter='port 53 or host 127.0.0.11 or not (src net 127.0.0.0/8 and dst net 127.0.0.0/8) and not ip6 host ::1'
  "$recorder" -i any -p -n -U --immediate-mode -s 0 -w "$pcap" "$filter" >/dev/null 2>"$log" &
  pid=$!
  limit="$(python3 -c 'import sys; print(max(1, int(float(sys.argv[1]) * 20)))' "${EGRESS_LISTEN_WAIT_S:-5}")"
  ready=0
  for ((i=0; i<limit; i++)); do
    if grep -q 'listening on' "$log"; then ready=1; break; fi
    if ! kill -0 "$pid" 2>/dev/null; then break; fi
    sleep 0.05
  done
  if [ "$ready" -eq 0 ]; then
    reason="$(tr '\n' ' ' <"$log")"
    kill -INT "$pid" 2>/dev/null || :
    wait "$pid" 2>/dev/null || :
    "$@"; rc=$?
    not_recorded "tcpdump not listening: $reason" || :
    if [ "$rc" -ne 0 ]; then return "$rc"; fi
    return 2
  fi
  "$@"; rc=$?
  python3 -c 'import socket, sys; s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.sendto(b"pfm-egress-end " + sys.argv[1].encode("ascii"), ("127.0.0.11", 9)); s.close()' "$stamp"
  end_limit="$(python3 -c 'import sys; print(max(1, int(float(sys.argv[1]) * 20)))' "${EGRESS_END_WAIT_S:-5}")"
  for ((i=0; i<end_limit; i++)); do
    if [ -f "$pcap" ] && grep -aFq "pfm-egress-end $stamp" "$pcap"; then break; fi
    if ! kill -0 "$pid" 2>/dev/null; then break; fi
    sleep 0.05
  done
  kill -INT "$pid" 2>/dev/null || :
  wait "$pid" 2>/dev/null || :
  check "$pcap" "$log"; verdict=$?
  if [ "$rc" -ne 0 ]; then return "$rc"; fi
  return "$verdict"
}

case "${1:-}" in
  check) [ "$#" -eq 3 ] || { echo 'usage: egress.sh check <pcap> <log>' >&2; exit 2; }; check "$2" "$3" ;;
  run) [ "$#" -ge 2 ] || { echo 'usage: egress.sh run <cmd…>' >&2; exit 2; }; shift; run "$@" ;;
  *) echo 'usage: egress.sh {run <cmd…>|check <pcap> <log>}' >&2; exit 2 ;;
esac
