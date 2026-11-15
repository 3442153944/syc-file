#!/bin/sh
# Isolated Docker network namespaces; never changes host forwarding or production WireGuard.
set -eu
prefix=filesync-mesh-nat
case "${1:-up}" in
  down)
    for name in peer-a peer-b router-a router-b relay; do docker rm -f "$prefix-$name" >/dev/null 2>&1 || true; done
    for name in lan-a lan-b wan; do docker network rm "$prefix-$name" >/dev/null 2>&1 || true; done
    exit 0;;
  up) ;;
  *) echo 'usage: symmetric-nat.sh [up|down]' >&2; exit 2;;
esac
for item in 'wan 10.230.0.0/24' 'lan-a 10.231.0.0/24' 'lan-b 10.232.0.0/24'; do
  set -- $item
  docker network create --subnet "$2" "$prefix-$1" >/dev/null
done
create() { docker run -d --name "$prefix-$1" --network "$prefix-$2" --ip "$3" ubuntu:24.04 sleep infinity >/dev/null; }
create relay wan 10.230.0.100
create router-a wan 10.230.0.2
create router-b wan 10.230.0.3
docker network connect --ip 10.231.0.2 "$prefix-lan-a" "$prefix-router-a"
docker network connect --ip 10.232.0.2 "$prefix-lan-b" "$prefix-router-b"
create peer-a lan-a 10.231.0.10
create peer-b lan-b 10.232.0.10
pid() { docker inspect -f '{{.State.Pid}}' "$prefix-$1"; }
net() { name=$1; shift; nsenter -t "$(pid "$name")" -n -- "$@"; }
for side in a b; do
  if [ "$side" = a ]; then lan=10.231.0.0/24; gateway=10.231.0.2; public=10.230.0.2; else lan=10.232.0.0/24; gateway=10.232.0.2; public=10.230.0.3; fi
  net "peer-$side" ip route replace default via "$gateway"
  net "router-$side" sysctl -w net.ipv4.ip_forward=1 >/dev/null
  net "router-$side" iptables -P FORWARD DROP
  net "router-$side" iptables -A FORWARD -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
  net "router-$side" iptables -A FORWARD -s "$lan" -j ACCEPT
  # Same local UDP socket gets different external ports for relay and peer destinations.
  net "router-$side" iptables -t nat -A POSTROUTING -s "$lan" -d 10.230.0.100 -p udp -j SNAT --to-source "$public:40010"
  net "router-$side" iptables -t nat -A POSTROUTING -s "$lan" -p udp -j SNAT --to-source "$public:40011"
done
echo 'symmetric NAT fixture ready: two peers, two destination-dependent routers, isolated relay'
echo "cleanup: $0 down"
