"""Probe and deliver actual HTTP messages to isolated local fixtures."""

from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import tempfile
import threading
import time
import unittest

from ops_monitor import COUNTERS, Monitor


class AlertTest(unittest.TestCase):
    def test_fault_suppression_recovery_and_probe_timeout(self):
        counts = dict.fromkeys(COUNTERS, 0)
        events = []
        delayed = threading.Event()

        class API(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_GET(self):
                if self.path == "/healthz":
                    if delayed.wait(0) is True:
                        time.sleep(0.25)
                        return
                    body = {"status": "ok"}
                elif self.path == "/internal/ops/metrics" and self.headers.get("Authorization") == "Bearer fixture":
                    body = counts
                else:
                    self.send_error(404)
                    return
                self.send_response(200)
                self.end_headers()
                self.wfile.write(json.dumps(body).encode())

        class Receiver(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_POST(self):
                events.append(json.loads(self.rfile.read(int(self.headers["Content-Length"]))))
                self.send_response(204)
                self.end_headers()

        def serve(handler):
            server = ThreadingHTTPServer(("127.0.0.1", 0), handler)
            worker = threading.Thread(target=server.serve_forever, daemon=True)
            worker.start()
            self.addCleanup(worker.join)
            self.addCleanup(server.server_close)
            self.addCleanup(server.shutdown)
            return f"http://127.0.0.1:{server.server_port}"

        base = serve(API)
        webhook = serve(Receiver)
        with tempfile.TemporaryDirectory() as directory:
            marker = Path(directory) / ".last-success"
            marker.touch()
            monitor = Monitor(base, "fixture", webhook, directory, timeout=0.1)
            monitor.tick()
            for _ in range(3):
                for key, threshold in COUNTERS.items():
                    counts[key] += threshold
                monitor.tick()
            self.assertCountEqual(events, [{"check": key, "state": "firing"} for key in COUNTERS])
            monitor.tick()
            monitor.tick()
            self.assertCountEqual(events[3:], [{"check": key, "state": "resolved"} for key in COUNTERS])
            marker.unlink()
            delayed.set()
            self.assertTrue(monitor.sample()["health"])
            for _ in range(3):
                monitor.tick()
            self.assertCountEqual(events[6:], [{"check": key, "state": "firing"} for key in ("health", "backup")])
            delayed.clear()
            marker.touch()
            monitor.tick()
            monitor.tick()
            self.assertCountEqual(events[8:], [{"check": key, "state": "resolved"} for key in ("health", "backup")])
            silent = Monitor(base)
            self.assertEqual(silent.webhook, "")


if __name__ == "__main__":
    unittest.main()
