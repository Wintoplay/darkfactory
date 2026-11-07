#!/usr/bin/env python3
"""Disposable OpenCode API: one chat and the question forms a probe writes.

The activity probe needs a prompt that is already waiting when the page loads,
which the real service cannot be told to produce on demand. It answers the three
routes the daemon's notification watcher reads and nothing else.
"""
import json
import os
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse

DIRECTORY = os.environ["DFB_STUB_DIR"]
FORMS = os.environ["DFB_STUB_FORMS"]
IDLE = os.environ["DFB_STUB_IDLE"]


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        path = urlparse(self.path).path
        if path == "/api/session":
            # idle is written by the probe: a change is how a finished turn is
            # detected, and the daemon must not ring for the value it primed on.
            try:
                with open(IDLE, encoding="utf-8") as handle:
                    idle = int(handle.read().strip() or "0")
            except (OSError, ValueError):
                idle = 0
            body = {"data": [{
                "id": "ses_q",
                "title": "Chat Q",
                "outcome": "succeeded",
                "location": {"directory": DIRECTORY},
                "time": {"updated": int(time.time() * 1000), "idle": idle},
            }]}
        elif path == "/api/session/ses_q/permission":
            body = {"data": []}
        elif path == "/api/session/ses_q/form":
            try:
                with open(FORMS, encoding="utf-8") as handle:
                    body = json.load(handle)
            except (OSError, ValueError):
                body = {"data": []}
        elif path == "/api/session/ses_q":
            body = {"data": {"id": "ses_q", "title": "Chat Q", "location": {"directory": DIRECTORY}}}
        elif path == "/api/session/ses_q/message":
            body = {"data": [
                {"id": "msg_1", "type": "user", "content": [{"type": "text", "text": "good morning"}]},
                {"id": "msg_2", "type": "assistant", "content": [
                    {"type": "text", "text": "Good morning! What can I help you with today?"}]},
            ]}
        else:
            self.send_error(404)
            return
        raw = json.dumps(body).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)


if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), Handler).serve_forever()
