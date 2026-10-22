#!/usr/bin/env python3
"""Fake CrowdSec Local API for scripts/iso/net-test.sh (task 104).

Usage: net-lapi.py <bind address> <port> <bouncer key> <decisions file>
  GET /v1/decisions/stream[?startup=true] with header X-Api-Key: the file
  holds one banned IP per line; startup=true answers all of them as "new",
  later calls answer what was added or removed since the previous call.
  A wrong key answers 403.
"""
import http.server
import json
import sys

BIND, PORT, KEY, FILE = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4]
sent = set()


def current():
    try:
        with open(FILE) as f:
            return {line.strip() for line in f if line.strip()}
    except FileNotFoundError:
        return set()


def decision(ip):
    return {"type": "ban", "scope": "Ip", "value": ip, "duration": "3h59m30s", "origin": "crowdsec", "scenario": "test"}


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        global sent
        if not self.path.startswith("/v1/decisions/stream") or self.headers.get("X-Api-Key") != KEY:
            self.send_response(403)
            self.end_headers()
            return
        now = current()
        if "startup=true" in self.path:
            new, deleted = now, set()
        else:
            new, deleted = now - sent, sent - now
        sent = now
        body = json.dumps({"new": [decision(i) for i in sorted(new)] or None,
                           "deleted": [decision(i) for i in sorted(deleted)] or None}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):
        sys.stderr.write("lapi: " + fmt % args + "\n")


http.server.ThreadingHTTPServer((BIND, PORT), Handler).serve_forever()
