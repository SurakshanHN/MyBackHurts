#!/usr/bin/env python3
from http.server import HTTPServer, BaseHTTPRequestHandler
import json, sys, urllib.parse

rps = 20.0
port = int(sys.argv[1]) if len(sys.argv) > 1 else 9000

class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        global rps
        u = urllib.parse.urlparse(self.path)
        if u.path == "/set":
            q = urllib.parse.parse_qs(u.query)
            if "rps" in q:
                rps = float(q["rps"][0])
            self._send({"status": "ok", "predicted_rps": rps})
        elif u.path == "/predict":
            self._send({"predicted_rps": rps})
        else:
            self.send_error(404)

    def do_GET(self):
        self.do_POST()

    def _send(self, data):
        b = json.dumps(data).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def log_message(self, *args):
        pass

if __name__ == "__main__":
    HTTPServer(("0.0.0.0", port), Handler).serve_forever()
