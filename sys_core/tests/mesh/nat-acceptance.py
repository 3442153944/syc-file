#!/usr/bin/env python3
"""Real WireGuard/NAT acceptance driven by sys_core/examples/mesh_plan.

Run on the authorized VM after symmetric-nat.sh up. Only touches the five
filesync-mesh-nat-* namespaces. Private keys never leave subprocess stdin.
No database, HTTP token, production interface, or host firewall is used.
"""
import argparse
import json
import os
import re
import subprocess
import time

PREFIX = "filesync-mesh-nat-"
NAMES = ("peer-a", "peer-b", "relay", "router-a", "router-b")


def command(args, data=None, check=True):
    return subprocess.run(args, input=data, text=True, capture_output=True, check=check)


class Lab:
    def __init__(self, planner):
        self.planner = planner
        self.pids = {}
        for name in NAMES:
            value = command(["docker", "inspect", "-f", "{{.State.Pid}}", PREFIX + name]).stdout.strip()
            assert value.isdigit() and int(value) > 1, "test container is not running: " + name
            self.pids[name] = value
        self.keys = {}
        self.attempts = {"a": {}, "b": {}}
        self.zone = 120
        self.port = 41010
        self.failures = []

    def net(self, name, *args, data=None, check=True):
        assert name in NAMES
        return command(["nsenter", "-t", self.pids[name], "-n", "--", *args], data, check)

    def setup(self):
        for name, address in (("peer-a", "172.1.50.1/32"), ("peer-b", "172.1.50.2/32"), ("relay", "172.1.0.254/16")):
            self.net(name, "ip", "link", "del", "natwg", check=False)
            self.net(name, "ip", "link", "add", "natwg", "type", "wireguard")
            self.net(name, "ip", "addr", "add", address, "dev", "natwg")
            private = command(["wg", "genkey"]).stdout
            self.keys[name] = command(["wg", "pubkey"], private).stdout.strip()
            self.net(name, "wg", "set", "natwg", "private-key", "/dev/stdin", "listen-port", "51820" if name == "relay" else "51821", data=private)
            del private
            self.net(name, "ip", "link", "set", "natwg", "up")
        for side, target in (("a", "172.1.50.2/32"), ("b", "172.1.50.1/32")):
            self.net("peer-" + side, "ip", "route", "replace", target, "dev", "natwg")
            self.net("peer-" + side, "ip", "route", "replace", "172.1.0.254/32", "dev", "natwg")
        self.net("relay", "wg", "set", "natwg", "peer", self.keys["peer-a"], "allowed-ips", "172.1.50.1/32", "peer", self.keys["peer-b"], "allowed-ips", "172.1.50.2/32")
        self.net("relay", "sysctl", "-w", "net.ipv4.ip_forward=1")
        if self.net("relay", "iptables", "-C", "FORWARD", "-i", "natwg", "-o", "natwg", "-j", "ACCEPT", check=False).returncode:
            self.net("relay", "iptables", "-A", "FORWARD", "-i", "natwg", "-o", "natwg", "-j", "ACCEPT")

    def nat(self, symmetric):
        # A fresh conntrack zone prevents old mappings surviving a topology change.
        # This is confined to the two fixture router network namespaces.
        self.zone += 1
        self.port += 10
        for side, lan, public in (("a", "10.231.0.0/24", "10.230.0.2"), ("b", "10.232.0.0/24", "10.230.0.3")):
            name = "router-" + side
            self.net(name, "iptables", "-t", "raw", "-F")
            self.net(name, "iptables", "-t", "raw", "-A", "PREROUTING", "-j", "CT", "--zone", str(self.zone))
            self.net(name, "iptables", "-t", "nat", "-F", "POSTROUTING")
            self.net(name, "iptables", "-t", "nat", "-F", "PREROUTING")
            if not symmetric:
                # SNAT alone is not an endpoint-independent inbound mapping:
                # an early unsolicited peer packet can create a local INPUT
                # conntrack entry and consume the reverse tuple. Model the fixed
                # external mapping explicitly; FORWARD still enforces filtering.
                target = "10.231.0.10:51821" if side == "a" else "10.232.0.10:51821"
                self.net(name, "iptables", "-t", "nat", "-A", "PREROUTING", "-d", public, "-p", "udp", "--dport", str(self.port), "-j", "DNAT", "--to-destination", target)
            if symmetric:
                self.net(name, "iptables", "-t", "nat", "-A", "POSTROUTING", "-s", lan, "-d", "10.230.0.100", "-p", "udp", "-j", "SNAT", "--to-source", f"{public}:{self.port}")
            self.net(name, "iptables", "-t", "nat", "-A", "POSTROUTING", "-s", lan, "-p", "udp", "-j", "SNAT", "--to-source", f"{public}:{self.port + (1 if symmetric else 0)}")
        print(json.dumps({"event": "nat", "symmetric": symmetric, "zone": self.zone, "relay_observed_port": self.port}), flush=True)

    def observations(self, side):
        name = "peer-" + side
        handshakes = dict(line.split() for line in self.net(name, "wg", "show", "natwg", "latest-handshakes").stdout.splitlines())
        endpoints = dict(line.split() for line in self.net(name, "wg", "show", "natwg", "endpoints").stdout.splitlines())
        return handshakes, endpoints

    def plan(self, side, round_number):
        other = "b" if side == "a" else "a"
        remote = "10.230.0.3" if side == "a" else "10.230.0.2"
        handshakes, endpoints = self.observations(side)
        peers = [
            {"pubkey": self.keys["peer-" + other], "allowed_ips": ["172.1.50.2/32" if side == "a" else "172.1.50.1/32"], "endpoint": f"{remote}:{self.port}", "candidates": [], "role": "member"},
            {"pubkey": self.keys["relay"], "allowed_ips": ["172.1.0.254/32"], "endpoint": "10.230.0.100:51820", "candidates": [], "role": "server"},
        ]
        now = time.time()
        for peer in peers:
            key = peer["pubkey"]
            attempt = self.attempts[side].get(key)
            handshake = int(handshakes.get(key, "0"))
            if attempt and handshake > attempt[1] and handshake > 0 and now - handshake < 180:
                # Endpoint must be observed on this peer, not guessed from an HTTP request.
                peer["confirmed_endpoint"] = endpoints.get(key)
        result = command([self.planner], json.dumps({"peers": peers, "round": round_number, "local_networks": []}) + "\n")
        return json.loads(result.stdout)

    def apply(self, side, routes):
        name = "peer-" + side
        wanted = {p["pubkey"] for p in routes}
        before_handshakes, _ = self.observations(side)
        existing = self.net(name, "wg", "show", "natwg", "peers").stdout.split()
        for key in existing:
            if key not in wanted:
                self.net(name, "wg", "set", "natwg", "peer", key, "remove")
        for route in routes:
            key, endpoint = route["pubkey"], route["endpoint"]
            previous = self.attempts[side].get(key)
            if not previous or previous[0] != endpoint:
                # Compare to the previous handshake, rather than rounding wall
                # clocks: a first successful handshake can arrive in this second.
                self.attempts[side][key] = (endpoint, int(before_handshakes.get(key, "0")))
            self.net(name, "wg", "set", "natwg", "peer", key, "allowed-ips", ",".join(route["allowed_ips"]), "endpoint", endpoint, "persistent-keepalive", "5")
        self.attempts[side] = {k: v for k, v in self.attempts[side].items() if k in wanted}

    def probe(self, label, round_number, expected=None):
        # Intentionally different client phases: they must not need synchronized clocks.
        plans = {side: self.plan(side, round_number + (1 if side == "b" else 0)) for side in ("a", "b")}
        for side in ("a", "b"):
            self.apply(side, plans[side])
        ping = self.net("peer-a", "ping", "-c", "2", "-W", "2", "172.1.50.2", check=False)
        owner = {side: next(("relay" if p["pubkey"] == self.keys["relay"] else "direct" for p in plans[side] if ("172.1.50.2/32" if side == "a" else "172.1.50.1/32") in p["allowed_ips"]), "missing") for side in ("a", "b")}
        ttls = sorted(set(map(int, re.findall(r"ttl=(\d+)", ping.stdout))))
        event = {"event": "probe", "label": label, "round": round_number, "owners": owner, "ping_ok": ping.returncode == 0, "ttls": ttls, "plans": plans}
        print(json.dumps(event), flush=True)
        if expected and (ping.returncode != 0 or any(v != expected for v in owner.values())):
            self.failures.append(label)
        if ping.returncode == 0 and len(set(owner.values())) == 1:
            expected_ttl = 64 if owner["a"] == "direct" else 63
            if ttls != [expected_ttl]:
                self.failures.append(label + "-unexpected-data-path")
        return owner, ping.returncode == 0

    def cleanup(self):
        for name in ("peer-a", "peer-b", "relay"):
            self.net(name, "ip", "link", "del", "natwg", check=False)
        for side, lan, public in (("a", "10.231.0.0/24", "10.230.0.2"), ("b", "10.232.0.0/24", "10.230.0.3")):
            name = "router-" + side
            self.net(name, "iptables", "-t", "raw", "-F")
            self.net(name, "iptables", "-t", "nat", "-F", "POSTROUTING")
            self.net(name, "iptables", "-t", "nat", "-F", "PREROUTING")
            self.net(name, "iptables", "-t", "nat", "-A", "POSTROUTING", "-s", lan, "-d", "10.230.0.100", "-p", "udp", "-j", "SNAT", "--to-source", public + ":40010")
            self.net(name, "iptables", "-t", "nat", "-A", "POSTROUTING", "-s", lan, "-p", "udp", "-j", "SNAT", "--to-source", public + ":40011")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("planner", help="built sys_core mesh_plan example")
    parser.add_argument("--tick", type=float, default=15, help="host planner cadence in seconds; production uses 15")
    parser.add_argument("--baseline", action="store_true", help="capture existing route loss, then exit after first probe")
    args = parser.parse_args()
    lab = Lab(os.path.abspath(args.planner))
    try:
        lab.setup()
        lab.nat(True)
        # Warm the server mappings without assigning the remote peer's route manually.
        for side in ("a", "b"):
            lab.apply(side, [{"pubkey": lab.keys["relay"], "allowed_ips": ["172.1.0.254/32"], "endpoint": "10.230.0.100:51820"}])
            lab.net("peer-" + side, "ping", "-c", "3", "-W", "3", "172.1.0.254")
        print(json.dumps({"event": "relay_endpoints", "value": lab.net("relay", "wg", "show", "natwg", "endpoints").stdout}), flush=True)
        if args.baseline:
            lab.probe("baseline-symmetric-probing", 0, "relay")
        else:
            for round_number in range(6):
                lab.probe("symmetric-stable-relay", round_number, "relay")
                time.sleep(args.tick)
            # Change mapping/filter topology. Existing authenticated sessions may be
            # stale: wait for direct confirmation rather than forcing route ownership.
            lab.nat(False)
            direct = False
            for round_number in range(6, 16):
                owner, ok = lab.probe("cone-auto-upgrade", round_number)
                if ok and all(v == "direct" for v in owner.values()):
                    direct = True
                    break
                time.sleep(args.tick)
            if not direct:
                lab.failures.append("cone-auto-upgrade")
            else:
                # Rebind back to destination-dependent NAT: old direct handshakes
                # must not pin an endpoint absent from the current candidates.
                lab.nat(True)
                time.sleep(6)  # let persistent keepalives establish fresh relay mappings
                for round_number in range(16, 20):
                    lab.probe("symmetric-rebind-auto-relay", round_number, "relay")
                    time.sleep(args.tick)
        print(json.dumps({"event": "result", "failures": lab.failures, "baseline": args.baseline}), flush=True)
        if lab.failures:
            raise SystemExit(1)
    finally:
        lab.cleanup()


if __name__ == "__main__":
    main()
