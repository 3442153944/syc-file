#!/usr/bin/env python3
"""Reviewable, pair-scoped relay firewall rule. No policy, sysctl or NAT changes.

Run check first. apply/remove require an explicitly authorized pair of mesh IPv4
addresses; this tool does not infer group permissions from membership alone.
"""
import argparse
import ipaddress
import re
import subprocess


def run(*args, check=True):
    return subprocess.run(args, check=check, text=True, capture_output=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("check", "apply", "remove"))
    parser.add_argument("interface")
    parser.add_argument("source")
    parser.add_argument("destination")
    args = parser.parse_args()
    if not re.fullmatch(r"[a-zA-Z0-9_-]{1,15}", args.interface):
        parser.error("invalid interface")
    network = ipaddress.ip_network("172.1.0.0/16")
    addresses = [ipaddress.IPv4Address(value) for value in (args.source, args.destination)]
    if any(ip not in network or ip in (network.network_address, network.broadcast_address) for ip in addresses) or addresses[0] == addresses[1]:
        parser.error("requires two different mesh host addresses")
    if args.action != "remove":
        # Only public WireGuard metadata; never inspect dump/showconf/private-key.
        prefixes = run("wg", "show", args.interface, "allowed-ips").stdout.split()
        if any(f"{ip}/32" not in prefixes for ip in addresses):
            raise SystemExit("Both addresses must belong to registered WireGuard peers")
        if run("sysctl", "-n", "net.ipv4.ip_forward").stdout.strip() != "1":
            raise SystemExit("IPv4 forwarding is disabled; operator must review host forwarding configuration")
    present = []
    for source, destination in (addresses, addresses[::-1]):
        rule = ("FORWARD", "-i", args.interface, "-o", args.interface,
                "-s", f"{source}/32", "-d", f"{destination}/32", "-m", "comment",
                "--comment", "filesync-reviewed-relay-pair", "-j", "ACCEPT")
        exists = run("iptables", "-w", "5", "-C", *rule, check=False).returncode == 0
        if args.action == "apply" and not exists:
            # Insert before existing UFW/Docker default drops, scoped to this pair.
            run("iptables", "-w", "5", "-I", rule[0], "1", *rule[1:])
            exists = True
        if args.action == "remove" and exists:
            run("iptables", "-w", "5", "-D", *rule)
            exists = False
        present.append(exists)
        print(f"{source} -> {destination}: {'allowed' if exists else 'no owned rule'}")
    if args.action == "check" and not all(present):
        raise SystemExit(1)


if __name__ == "__main__":
    main()
