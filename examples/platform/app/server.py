"""Demo app: JSON API backed by Postgres + ClickHouse, plus a static frontend."""
import json
import os
import sys
import urllib.parse
import urllib.request
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer

import psycopg

DATABASE_URL = os.environ["DATABASE_URL"]
CLICKHOUSE_URL = os.environ["CLICKHOUSE_URL"]
STATIC = os.path.join(os.path.dirname(os.path.abspath(__file__)), "static")


def ch(query, data=None):
    # POST: ClickHouse treats GET requests as read-only.
    if data is None:
        url, body = CLICKHOUSE_URL + "/", query.encode()
    else:
        url, body = CLICKHOUSE_URL + "/?" + urllib.parse.urlencode({"query": query}), data
    with urllib.request.urlopen(url, data=body, timeout=5) as resp:
        return resp.read().decode().strip()


def migrate():
    with psycopg.connect(DATABASE_URL) as conn:
        conn.execute("CREATE TABLE IF NOT EXISTS visits (id serial PRIMARY KEY, at timestamptz DEFAULT now())")
    ch("CREATE TABLE IF NOT EXISTS events (at DateTime DEFAULT now(), kind String) ENGINE = MergeTree ORDER BY at")


class Handler(SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=STATIC, **kwargs)

    def send_json(self, payload, status=200):
        raw = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def stats(self):
        with psycopg.connect(DATABASE_URL) as conn:
            visits = conn.execute("SELECT count(*) FROM visits").fetchone()[0]
            pg_version = conn.execute("SHOW server_version").fetchone()[0]
        return {
            "visits": visits,
            "events": int(ch("SELECT count() FROM events")),
            "postgres": pg_version,
            "clickhouse": ch("SELECT version()"),
            "container": os.uname().nodename,
            "version": os.environ.get("APP_VERSION", "1"),
        }

    def do_GET(self):
        if self.path == "/healthz":
            try:
                with psycopg.connect(DATABASE_URL, connect_timeout=2) as conn:
                    conn.execute("SELECT 1")
                ch("SELECT 1")
            except Exception as exc:  # noqa: BLE001
                return self.send_json({"ok": False, "error": str(exc)}, 503)
            return self.send_json({"ok": True})
        if self.path == "/api/stats":
            return self.send_json(self.stats())
        return super().do_GET()

    def do_POST(self):
        if self.path != "/api/visit":
            return self.send_json({"error": "not found"}, 404)
        with psycopg.connect(DATABASE_URL) as conn:
            conn.execute("INSERT INTO visits DEFAULT VALUES")
        ch("INSERT INTO events (kind) FORMAT TSV", b"visit\n")
        return self.send_json(self.stats())


if __name__ == "__main__":
    if sys.argv[1:] == ["migrate"]:
        migrate()
        raise SystemExit(0)
    port = int(os.environ.get("PORT", "8080"))
    print(f"listening on :{port}", flush=True)
    ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()
