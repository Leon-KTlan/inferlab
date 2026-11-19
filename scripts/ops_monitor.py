"""Independent InferLab monitor. Start on the deployment host or another trusted host."""

import json
from http.client import HTTPException
import os
from pathlib import Path
import sys
import time
from urllib.parse import urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener

COUNTERS = {"http_5xx": 5, "task_failures": 3, "payment_failures": 3}


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def endpoint(value, origin=False):
    url = urlsplit(value)
    local = url.hostname in {"127.0.0.1", "::1", "localhost"}
    if (not url.hostname or url.username is not None or url.fragment or
            (url.scheme != "https" and not (url.scheme == "http" and local)) or
            (origin and (url.path not in ("", "/") or url.query))):
        raise ValueError("invalid monitor endpoint configuration")
    return value.rstrip("/") if origin else value


class Monitor:
    def __init__(self, base, token="", webhook="", backup_dir=None, timeout=3, max_backup_age=86400):
        self.base = endpoint(base, origin=True)
        self.token = token
        self.webhook = endpoint(webhook) if webhook else ""
        self.backup_dir = Path(backup_dir) if backup_dir else None
        self.timeout = timeout
        self.max_backup_age = max_backup_age
        self.previous = None
        self.observed = {}
        self.delivered = set()
        self.opener = build_opener(NoRedirect())

    def request(self, url, token="", payload=None):
        headers = {"Authorization": "Bearer " + token} if token else {}
        data = None
        if payload is not None:
            headers["Content-Type"] = "application/json"
            data = json.dumps(payload).encode()
        with self.opener.open(Request(url, data=data, headers=headers), timeout=self.timeout) as response:
            if payload is not None:
                return
            body = response.read(4097)
            if len(body) > 4096:
                raise ValueError("oversized probe response")
            return json.loads(body)

    def sample(self):
        try:
            health = self.request(self.base + "/healthz")
            result = {"health": health.get("status") != "ok"}
        except (OSError, HTTPException, ValueError, AttributeError):
            result = {"health": True}
        if self.token and not result["health"]:
            try:
                metrics = self.request(self.base + "/internal/ops/metrics", self.token)
                if not isinstance(metrics, dict) or any(type(metrics.get(k)) is not int or metrics[k] < 0 for k in COUNTERS):
                    raise ValueError("invalid metrics")
                result["metrics"] = False
                for k, threshold in COUNTERS.items():
                    if self.previous is not None:
                        result[k] = metrics[k] - self.previous[k] >= threshold if metrics[k] >= self.previous[k] else metrics[k] >= threshold
                self.previous = metrics
            except (OSError, HTTPException, ValueError):
                result["metrics"] = True
        if self.backup_dir is not None:
            try:
                latest = (self.backup_dir / ".last-success").stat().st_mtime
                result["backup"] = time.time() - latest > self.max_backup_age
            except OSError:
                result["backup"] = True
        return result

    def tick(self):
        for check, failed in self.sample().items():
            streak = self.observed.get(check, 0)
            streak = min(2, max(streak, 0) + 1) if failed else max(-2, min(streak, 0) - 1)
            if streak == 2 and check not in self.delivered:
                if self.notify(check, "firing"):
                    self.delivered.add(check)
            elif streak == -2 and check in self.delivered:
                if self.notify(check, "resolved"):
                    self.delivered.remove(check)
            self.observed[check] = streak

    def notify(self, check, state):
        print(f"InferLab monitor: {check} {state}", flush=True)
        if not self.webhook:
            return True
        try:
            self.request(self.webhook, os.getenv("OPS_WEBHOOK_TOKEN", ""), {"check": check, "state": state})
            return True
        except (OSError, HTTPException):
            print("InferLab monitor: notification delivery failed", file=sys.stderr, flush=True)
            return False


def main():
    try:
        interval = int(os.getenv("OPS_INTERVAL_SECONDS", "60"))
        backup_age = int(os.getenv("OPS_MAX_BACKUP_AGE_SECONDS", "86400"))
        if interval < 1 or backup_age < 1:
            raise ValueError
        monitor = Monitor(os.environ["OPS_BASE_URL"], os.getenv("OPS_METRICS_TOKEN", ""),
                          os.getenv("OPS_WEBHOOK_URL", ""), os.getenv("OPS_BACKUP_DIR"), max_backup_age=backup_age)
    except (KeyError, ValueError):
        sys.exit("invalid InferLab monitor configuration")
    while True:
        monitor.tick()
        time.sleep(interval)


if __name__ == "__main__":
    main()
