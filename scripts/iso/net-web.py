#!/usr/bin/env python3
"""Tiny HTTP server for scripts/iso/net-test.sh (task 039).

Usage: net-web.py <bind address> <port> <name>
  /whoami   -> "<name> sees <client ip>" (shows whether NAT happened)
  /feed.txt -> an IP threat feed listing 203.0.113.82
  /id.txt   -> "uid=0(root) ..." (triggers the IPS test signature, sid 2100498)
  /geoip.csv.gz -> a country database putting 203.0.113.83 in New Zealand
  anything else -> "hello from <name>"
"""
import gzip
import http.server
import socket
import sys

BIND, PORT, NAME = sys.argv[1], int(sys.argv[2]), sys.argv[3]


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/whoami":
            body = f"{NAME} sees {self.client_address[0]}\n"
        elif self.path == "/feed.txt":
            body = "# goblin gate test feed\n203.0.113.82\n"
        elif self.path == "/id.txt":
            body = "uid=0(root) gid=0(root) groups=0(root)\n"
        elif self.path == "/geoip.csv.gz":
            body = None
            data = gzip.compress(b"203.0.113.83,203.0.113.83,NZ\n2001:db8:0:1::83,2001:db8:0:1::83,NZ\n")
        else:
            body = f"hello from {NAME}\n"
        if body is not None:
            data = body.encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, fmt, *args):
        sys.stderr.write(f"{NAME}: {self.client_address[0]} {fmt % args}\n")


class Server(http.server.ThreadingHTTPServer):
    # IPv6 bind addresses need an AF_INET6 socket (task 045)
    address_family = socket.AF_INET6 if ":" in BIND else socket.AF_INET


Server((BIND, PORT), Handler).serve_forever()
