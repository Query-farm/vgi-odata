"""Replay unchanged Northwind sample responses through the actual OData worker."""

import hashlib
import json
from datetime import UTC, datetime
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from urllib.parse import parse_qs, urlparse


class PublicService:
    """Keep service identity explicit and fail requests outside the recorded contract."""

    def __init__(self, mode, service_url):
        if mode not in ("replay", "live"):
            raise ValueError("DATA_MODE must be replay or live")
        folder = Path(__file__).resolve().parent / "recordings"
        self.provenance = json.loads((folder / "provenance.json").read_text())
        if service_url != self.provenance["service_url"]:
            raise ValueError("This example records only the selected Northwind v4 service")
        self.mode = mode
        self.url = service_url
        self.started_at = datetime.now(UTC).isoformat()
        self.requests = []
        self.server = self.thread = None
        bodies = {}
        for record in self.provenance["records"]:
            body = (folder / record["file"]).read_bytes()
            if hashlib.sha256(body).hexdigest() != record["sha256"]:
                raise ValueError("Public response checksum mismatch")
            bodies[record["route"]] = (record, body)
        if mode == "live":
            return
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_GET(self):
                route = urlparse(self.path)
                found = bodies.get(route.path.removeprefix("/odata"))
                owner.requests.append(self.path)
                valid = route.path.startswith("/odata") and found is not None
                if valid:
                    record, body = found
                    valid = parse_qs(route.query) == {k: [v] for k, v in record["query"].items()}
                    valid = valid and record["accept"] in self.headers.get("Accept", "")
                if not valid:
                    self.send_error(404, "Request outside recorded Northwind contract")
                    return
                self.send_response(200)
                self.send_header("Content-Type", record["content_type"])
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.server.server_port}/odata"
        self.thread = Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def close(self):
        if self.server:
            self.server.shutdown()
            self.server.server_close()
            self.thread.join(timeout=5)
