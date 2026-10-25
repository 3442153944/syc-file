#!/usr/bin/env python3
"""Reproduce FORWARD DROP in disposable Docker relay, verify scoped repair."""
import argparse
import importlib.util
import pathlib
import time

parser = argparse.ArgumentParser()
parser.add_argument("planner")
parser.add_argument("firewall_tool")
args = parser.parse_args()
spec = importlib.util.spec_from_file_location("mesh_lab", pathlib.Path(__file__).with_name("nat-acceptance.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
lab = module.Lab(args.planner)
tool = str(pathlib.Path(args.firewall_tool).resolve())
policy = None
try:
    lab.setup()
    policy = lab.net("relay", "iptables", "-S", "FORWARD").stdout.splitlines()[0].split()[-1]
    lab.nat(symmetric=True)
    # Configure through the real sys_core planner; unconfirmed direct peers keep
    # destinations on relay. No manual AllowedIPs substitution here.
    lab.apply("a", lab.plan("a", 0))
    lab.apply("b", lab.plan("b", 0))
    time.sleep(4)
    lab.net("peer-a", "ping", "-c", "2", "-W", "3", "172.1.50.2")
    rule = ("FORWARD", "-i", "natwg", "-o", "natwg", "-j", "ACCEPT")
    while lab.net("relay", "iptables", "-C", *rule, check=False).returncode == 0:
        lab.net("relay", "iptables", "-D", *rule)
    lab.net("relay", "iptables", "-P", "FORWARD", "DROP")
    blocked = lab.net("peer-a", "ping", "-c", "2", "-W", "2", "172.1.50.2", check=False)
    assert blocked.returncode != 0, "FORWARD DROP must block peer relay"
    print("PASS: server handshake alone cannot bypass FORWARD DROP", flush=True)
    cmd = ("python3", tool, "apply", "natwg", "172.1.50.1", "172.1.50.2")
    lab.net("relay", *cmd)
    lab.net("relay", *cmd)  # idempotence
    output = lab.net("peer-a", "ping", "-c", "3", "-W", "3", "172.1.50.2").stdout
    assert "ttl=63" in output.lower(), output
    print(output, flush=True)
    print("PASS: explicit pair restores relayed peer traffic (TTL63)", flush=True)
    lab.net("relay", "python3", tool, "remove", "natwg", "172.1.50.1", "172.1.50.2")
    assert lab.net("peer-a", "ping", "-c", "1", "-W", "2", "172.1.50.2", check=False).returncode != 0
    print("PASS: remove restores default isolation", flush=True)
finally:
    lab.net("relay", "python3", tool, "remove", "natwg", "172.1.50.1", "172.1.50.2", check=False)
    if policy in ("ACCEPT", "DROP"):
        lab.net("relay", "iptables", "-P", "FORWARD", policy)
    lab.cleanup()
