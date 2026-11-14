"""Small read-only OData v4 inventory fixture with observable paging."""

from __future__ import annotations

import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Lock, Thread
from urllib.parse import parse_qs, urlparse

METADATA = """<?xml version="1.0" encoding="utf-8"?>
<edmx:Edmx Version="4.0" xmlns:edmx="http://docs.oasis-open.org/odata/ns/edmx"><edmx:DataServices>
<Schema Namespace="Notebook" xmlns="http://docs.oasis-open.org/odata/ns/edm">
<EntityType Name="Product"><Key><PropertyRef Name="ProductID"/></Key>
<Property Name="ProductID" Type="Edm.Int32" Nullable="false"/>
<Property Name="Name" Type="Edm.String" Nullable="false"/>
<Property Name="Category" Type="Edm.String" Nullable="false"/>
<Property Name="Stock" Type="Edm.Int32" Nullable="true"/>
<Property Name="Target" Type="Edm.Int32" Nullable="false"/>
</EntityType><EntityContainer Name="Inventory"><EntitySet Name="Products" EntityType="Notebook.Product"/>
</EntityContainer></Schema></edmx:DataServices></edmx:Edmx>"""


class InventoryService:
    """Serve invented products in fixed three-entity pages and log read requests."""

    def __init__(self):
        """Start a loopback-only fixture; no routes implement writes."""
        self.products = json.loads(Path("fixtures/products.json").read_text())
        self.phase = "discovery"
        self.requests = []
        self.lock = Lock()
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, format, *args):
                pass

            def do_GET(self):
                route = urlparse(self.path)
                query = parse_qs(route.query)
                content_type = "application/json"
                audit = None
                if route.path in ("/odata", "/odata/") and not query:
                    payload = {
                        "@odata.context": owner.url + "/$metadata",
                        "value": [{"name": "Products", "kind": "EntitySet", "url": "Products"}],
                    }
                    body = json.dumps(payload).encode()
                elif route.path == "/odata/$metadata" and not query:
                    if "xml" not in self.headers.get("Accept", ""):
                        self.send_error(415, "Metadata requires XML Accept")
                        return
                    body = METADATA.encode()
                    content_type = "application/xml"
                elif route.path == "/odata/Products":
                    if query == {"$orderby": ["ProductID"]}:
                        offset = 0
                    elif query in ({"$skiptoken": ["3"]}, {"$skiptoken": ["6"]}):
                        offset = int(query["$skiptoken"][0])
                    else:
                        self.send_error(400, "Fixture supports ProductID order and its own continuation links only")
                        return
                    page = owner.products[offset : offset + 3]
                    payload = {"@odata.context": owner.url + "/$metadata#Products", "value": page}
                    has_next = offset + len(page) < len(owner.products)
                    if has_next:
                        payload["@odata.nextLink"] = f"Products?$skiptoken={offset + 3}"
                    body = json.dumps(payload).encode()
                    audit = {
                        "phase": owner.phase,
                        "page_offset": offset,
                        "entities_served": len(page),
                        "has_next": has_next,
                    }
                else:
                    self.send_error(404, "Unknown fixture route")
                    return
                if audit is not None:
                    with owner.lock:
                        owner.requests.append(audit)
                self.send_response(200)
                self.send_header("Content-Type", content_type)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.server.server_port}/odata"
        self.thread = Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def save_audit(self):
        """Write observed fixture requests for visible SQL accounting."""
        folder = Path("validation")
        folder.mkdir(exist_ok=True)
        with self.lock:
            body = json.dumps(self.requests, indent=2)
        (folder / "request_audit.json").write_text(body + "\n")

    def close(self):
        """Stop the local fixture server."""
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=5)
