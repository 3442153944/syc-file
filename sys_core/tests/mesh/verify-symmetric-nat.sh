#!/bin/sh
set -eu
prefix=filesync-mesh-nat
pid() { docker inspect -f '{{.State.Pid}}' "$prefix-$1"; }
net() { name=$1; shift; nsenter -t "$(pid "$name")" -n -- "$@"; }
# Ubuntu's wg AppArmor profile permits keys under /etc/wireguard, not /tmp.
keys=$(mktemp -d /etc/wireguard/filesync-mesh-test.XXXXXX)
trap 'rm -f "$keys/a" "$keys/b" "$keys/relay"; rmdir "$keys"' EXIT
umask 077
for side in a b relay; do wg genkey > "$keys/$side"; done
a_pub=$(wg pubkey < "$keys/a"); b_pub=$(wg pubkey < "$keys/b"); relay_pub=$(wg pubkey < "$keys/relay")
for name in peer-a peer-b relay; do net "$name" ip link del natwg 2>/dev/null || true; net "$name" ip link add natwg type wireguard; done
net peer-a ip addr add 172.1.50.1/32 dev natwg
net peer-b ip addr add 172.1.50.2/32 dev natwg
net relay ip addr add 172.1.0.254/16 dev natwg
net peer-a wg set natwg private-key /dev/stdin listen-port 51821 peer "$relay_pub" allowed-ips 172.1.0.254/32 endpoint 10.230.0.100:51820 persistent-keepalive 1 < "$keys/a"
net peer-b wg set natwg private-key /dev/stdin listen-port 51821 peer "$relay_pub" allowed-ips 172.1.0.254/32 endpoint 10.230.0.100:51820 persistent-keepalive 1 < "$keys/b"
net relay wg set natwg private-key /dev/stdin listen-port 51820 peer "$a_pub" allowed-ips 172.1.50.1/32 peer "$b_pub" allowed-ips 172.1.50.2/32 < "$keys/relay"
for name in peer-a peer-b relay; do net "$name" ip link set natwg up; done
net relay sysctl -w net.ipv4.ip_forward=1 >/dev/null
net relay iptables -A FORWARD -i natwg -o natwg -j ACCEPT
net peer-a ip route add 172.1.0.254/32 dev natwg
net peer-b ip route add 172.1.0.254/32 dev natwg
net peer-a ip route add 172.1.50.2/32 dev natwg
net peer-b ip route add 172.1.50.1/32 dev natwg
# Establish both relay mappings before attempting peer endpoints learned there.
net peer-a ping -c 3 -W 3 172.1.0.254
net peer-b ping -c 3 -W 3 172.1.0.254
echo 'observed relay endpoints:'
net relay wg show natwg endpoints
net peer-a wg set natwg peer "$b_pub" allowed-ips 172.1.50.2/32 endpoint 10.230.0.3:40010 persistent-keepalive 1
net peer-b wg set natwg peer "$a_pub" allowed-ips 172.1.50.1/32 endpoint 10.230.0.2:40010 persistent-keepalive 1
if net peer-a ping -c 2 -W 2 172.1.50.2 >/dev/null; then echo 'ERROR: symmetric NAT direct unexpectedly passed' >&2; exit 1; fi
echo 'PASS: destination-dependent NAT blocks direct peer path'
net peer-a wg set natwg peer "$b_pub" remove peer "$relay_pub" allowed-ips 172.1.0.254/32,172.1.50.2/32
net peer-b wg set natwg peer "$a_pub" remove peer "$relay_pub" allowed-ips 172.1.0.254/32,172.1.50.1/32
net peer-a ping -c 3 -W 3 172.1.50.2
echo 'PASS: relay fallback restores peer connectivity'
net relay wg show natwg transfer
