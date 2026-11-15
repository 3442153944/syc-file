"""Temporary UDP-over-SSH fixture: edge UDP -> loopback TCP tunnel -> origin UDP.

This deliberately loses original client IP at the WG origin. It is a test fixture,
not the production relay. WireGuard payloads remain encrypted end to end.
"""
import argparse
import socket
import struct
import threading


def read_exact(sock, count):
    result = bytearray()
    while len(result) < count:
        chunk = sock.recv(count - len(result))
        if not chunk:
            raise EOFError
        result.extend(chunk)
    return bytes(result)


def frames(sock):
    while True:
        size = struct.unpack("!H", read_exact(sock, 2))[0]
        yield read_exact(sock, size)


def send(sock, payload):
    sock.sendall(struct.pack("!H", len(payload)) + payload)


def origin(port, host, udp_port):
    def connection(stream):
        udp = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        udp.connect((host, udp_port))
        stream.settimeout(120)
        udp.settimeout(120)
        def replies():
            try:
                while True:
                    send(stream, udp.recv(65535))
            except (OSError, EOFError):
                stream.close()
                udp.close()
        threading.Thread(target=replies, daemon=True).start()
        try:
            for payload in frames(stream):
                udp.send(payload)
        except (OSError, EOFError):
            pass
        finally:
            stream.close()
            udp.close()
    listener = socket.socket()
    listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    listener.bind(("127.0.0.1", port))
    listener.listen(32)
    while True:
        stream, _ = listener.accept()
        stream.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
        threading.Thread(target=connection, args=(stream,), daemon=True).start()


def edge(port, tcp_port):
    udp = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    udp.bind(("0.0.0.0", port))
    channels = {}
    lock = threading.Lock()
    def replies(address, stream):
        try:
            for payload in frames(stream):
                udp.sendto(payload, address)
        except (OSError, EOFError):
            pass
        finally:
            with lock:
                if channels.get(address) is stream:
                    del channels[address]
            stream.close()
    while True:
        payload, address = udp.recvfrom(65535)
        with lock:
            stream = channels.get(address)
            if stream is None:
                if len(channels) >= 32:
                    continue
                try:
                    stream = socket.create_connection(("127.0.0.1", tcp_port), timeout=3)
                    stream.settimeout(120)
                    stream.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
                except OSError:
                    continue
                channels[address] = stream
                threading.Thread(target=replies, args=(address, stream), daemon=True).start()
        try:
            send(stream, payload)
        except OSError:
            stream.close()
            with lock:
                channels.pop(address, None)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("mode", choices=("origin", "edge"))
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--target-host", default="192.168.31.120")
    parser.add_argument("--target-port", type=int, required=True)
    args = parser.parse_args()
    if args.mode == "origin":
        origin(args.port, args.target_host, args.target_port)
    else:
        edge(args.port, args.target_port)
