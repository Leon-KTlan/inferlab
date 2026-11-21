"""Run reproducible InferLab Mock experiments in an isolated local process."""

import argparse
from contextlib import closing, contextmanager
import csv
from datetime import datetime, timezone
import hashlib
import http.client
from http.cookies import SimpleCookie
import json
import math
import platform
from pathlib import Path
import socket
import sqlite3
import statistics
import subprocess
import tempfile
import time


ROOT = Path(__file__).resolve().parents[1]
SCENARIOS = {"baseline-v1": (160, "pass"), "slow-v1": (420, "fail")}
STEPS = (1, 4, 8)
SLO = {"p95_e2e_ms": 250, "error_rate_max": 0.05}


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def read_command(*command):
    return subprocess.check_output(command, cwd=ROOT, text=True).strip()


class LocalAPI:
    def __init__(self, port):
        self.port = port
        self.cookie = SimpleCookie()

    def request(self, method, path, expected=200, body=None):
        headers = {"Origin": f"http://127.0.0.1:{self.port}"}
        if self.cookie:
            headers["Cookie"] = "; ".join(morsel.OutputString() for morsel in self.cookie.values())
        if body is not None:
            headers["Content-Type"] = "application/json"
            body = json.dumps(body).encode()
        with closing(http.client.HTTPConnection("127.0.0.1", self.port, timeout=3)) as conn:
            conn.request(method, path, body, headers)
            response = conn.getresponse()
            for name, value in response.getheaders():
                if name.lower() == "set-cookie":
                    cookie = SimpleCookie(value)
                    for key, morsel in cookie.items():
                        self.cookie[key] = morsel.value
            data = json.loads(response.read())
            require(response.status == expected, f"{method} {path}: HTTP {response.status}: {data.get('error', {}).get('code', 'UNKNOWN')}")
            return data


@contextmanager
def start_api(binary, database):
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    env = {
        "APP_ENV": "local", "DB_PATH": str(database),
        "LISTEN_ADDR": f"127.0.0.1:{port}", "PAYMENTS_ENABLED": "false",
    }
    with subprocess.Popen([str(binary)], cwd=database.parent, env=env,
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL) as process:
        try:
            api = LocalAPI(port)
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                require(process.poll() is None, "isolated API exited before readiness")
                try:
                    health = api.request("GET", "/healthz")
                    require(health["status"] == "ok", "isolated API unhealthy")
                    break
                except (OSError, http.client.HTTPException):
                    time.sleep(0.05)
            else:
                raise RuntimeError("isolated API readiness timed out")
            require(process.poll() is None and database.is_file(), "isolated API did not start")
            yield api
        finally:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=12)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()


def configuration(scene, stage_seconds):
    return {
        "name": f"Thesis {scene}",
        "target": {"type": "mock", "model": "mock-llm", "mock_scenario": scene},
        "workload": {
            "concurrency_steps": list(STEPS), "stage_seconds": stage_seconds,
            "max_rps": 8, "max_requests": 800, "timeout_ms": 1000,
        },
        "slo": SLO,
    }


def wait_report(api, run_id, workload):
    base = f"/api/v1/runs/{run_id}"
    timeout = len(STEPS) * (workload["stage_seconds"] + workload["timeout_ms"] / 1000) + 10
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        run = api.request("GET", base)
        if run["status"] == "succeeded":
            return api.request("GET", base + "/report")
        require(run["status"] in ("queued", "running"), f"{run_id}: {run['status']}, {run.get('error_code', '')}")
        time.sleep(0.25)
    raise RuntimeError(f"{run_id}: timed out waiting for report")


def read_samples(database, run_id):
    uri = database.as_uri() + "?mode=ro"
    with closing(sqlite3.connect(uri, uri=True)) as conn:
        samples = []
        for stage, data in conn.execute("SELECT stage, data FROM samples WHERE run_id = ? ORDER BY id", (run_id,)):
            sample = json.loads(data)
            require(stage == sample["stage"], f"{run_id}: inconsistent sample stage")
            samples.append(sample)
    return samples


def verify_metrics(metrics, samples, label):
    count = len(samples)
    errors = sum(bool(sample.get("error_code")) for sample in samples)
    latencies = sorted(sample["latency_ms"] for sample in samples)
    require(all(math.isfinite(value) and value >= 0 for value in latencies), f"{label}: invalid sample latency")
    require(metrics["duration_seconds"] > 0, f"{label}: missing observation window")
    expected = {
        "request_count": count, "completed_count": count, "success_count": count - errors,
        "error_count": errors, "error_rate": errors / count if count else 0,
        "p50_e2e_ms": latencies[math.ceil(count * .50) - 1] if count else 0,
        "p95_e2e_ms": latencies[math.ceil(count * .95) - 1] if count else 0,
        "throughput_rps": count / metrics["duration_seconds"],
    }
    for key, value in expected.items():
        require(math.isclose(metrics[key], value, rel_tol=1e-9, abs_tol=1e-9),
                f"{label}: {key} reported {metrics[key]}, recalculated {value}")


def verify_report(report, samples, config):
    run_id = report["run_id"]
    require(report["source_type"] == "mock" and report["config"] == config, f"{run_id}: snapshot mismatch")
    stages = report["stage_summaries"]
    require([stage["concurrency"] for stage in stages] == list(STEPS), f"{run_id}: unexpected concurrency steps")
    require([stage["stage"] for stage in stages] == list(range(1, len(STEPS) + 1)), f"{run_id}: incomplete stages")
    require(all(1 <= sample["stage"] <= len(STEPS) for sample in samples), f"{run_id}: unknown sample stage")
    verify_metrics(report, samples, run_id)
    limits = config["slo"]
    quick_check = config["workload"]["stage_seconds"] == 1
    expected_status = SCENARIOS[config["target"]["mock_scenario"]][1]
    statuses = []
    for stage in stages:
        stage_id = stage["stage"]
        label = f"{run_id} stage {stage_id}"
        require(stage["duration_seconds"] >= config["workload"]["stage_seconds"], f"{label}: truncated stage")
        stage_samples = [sample for sample in samples if sample["stage"] == stage_id]
        verify_metrics(stage, stage_samples, label)
        if len(stage_samples) < 20:
            status = "insufficient_data"
        elif stage["p95_e2e_ms"] > limits["p95_e2e_ms"] or stage["error_rate"] > limits["error_rate_max"]:
            status = "fail"
        else:
            status = "pass"
        require(stage["slo_status"] == status, f"{label}: SLO mismatch")
        enough_samples = status != "insufficient_data"
        require(enough_samples or quick_check, f"{label}: fewer than 20 completed samples")
        require(not enough_samples or status == expected_status, f"{label}: expected {expected_status}, observed {status}")
        require(stage["error_count"] == 0, f"{label}: expected zero errors, observed {stage['error_count']}")
        statuses.append(status)
    overall = next(status for status in ("fail", "insufficient_data", "pass") if status in statuses)
    require(report["slo_status"] == overall, f"{run_id}: overall SLO mismatch")


def run_experiments(api, database, args):
    configs = {scene: configuration(scene, args.stage_seconds) for scene in SCENARIOS}
    first_run = {}
    records = []
    for round_no in range(1, args.repeats + 1):
        scenes = list(configs) if round_no % 2 else list(reversed(configs))
        for scene in scenes:
            config = configs[scene]
            source = first_run.get(scene)
            print(f"Round {round_no}/{args.repeats}: {scene} ({'rerun' if source else 'first run'})", flush=True)
            if source:
                run = api.request("POST", f"/api/v1/runs/{source}/rerun", expected=202)
            else:
                experiment = api.request("POST", "/api/v1/experiments", expected=201, body=config)
                run = api.request("POST", f"/api/v1/experiments/{experiment['experiment_id']}/runs", expected=202)
                first_run[scene] = run["run_id"]
            run_id = run["run_id"]
            require(all(record["report"]["run_id"] != run_id for record in records), f"{run_id}: run ID reused")
            report = wait_report(api, run_id, config["workload"])
            samples = read_samples(database, run_id)
            verify_report(report, samples, config)
            records.append({"round": round_no, "scene": scene, "rerun_from": source, "report": report, "samples": samples})
            print(f"  Verified {len(samples)} samples, P95 {report['p95_e2e_ms']:.2f} ms, SLO {report['slo_status']}", flush=True)
    return records


def write_chart(path, records):
    medians = {
        scene: [statistics.median(
            record["report"]["stage_summaries"][i]["p95_e2e_ms"]
            for record in records if record["scene"] == scene)
            for i in range(len(STEPS))]
        for scene in SCENARIOS
    }
    ceiling = max(500, math.ceil(max(max(values) for values in medians.values()) / 100) * 100)
    x = (95, 315, 535)

    def y(value):
        return 256 - value / ceiling * 185

    svg = [
        '<svg xmlns="http://www.w3.org/2000/svg" width="640" height="365" viewBox="0 0 640 365" role="img" aria-label="Mock P95 by concurrency limit">',
        '<rect width="640" height="365" fill="white"/>',
        '<g font-family="sans-serif" font-size="13" fill="#172033">',
        '<text x="48" y="35" font-size="20" font-weight="bold">Mock P95 by concurrency limit</text>',
        '<text x="48" y="57">Median of run-level P95 values (ms)</text>',
    ]
    for rate in range(6):
        tick = ceiling * rate / 5
        svg.append(f'<line x1="72" x2="580" y1="{y(tick):.2f}" y2="{y(tick):.2f}" stroke="#dfe4ec"/>')
        svg.append(f'<text x="65" y="{y(tick) + 4:.2f}" text-anchor="end">{tick:g}</text>')
    threshold_y = y(SLO["p95_e2e_ms"])
    svg.append(f'<line x1="72" x2="580" y1="{threshold_y:.2f}" y2="{threshold_y:.2f}" stroke="#6d5875" stroke-dasharray="5 4"/>')
    svg.append(f'<text x="586" y="{threshold_y + 4:.2f}" fill="#6d5875">SLO</text>')
    for point, step in zip(x, STEPS):
        svg.append(f'<text x="{point}" y="280" text-anchor="middle">{step}</text>')
    for i, (scene, color) in enumerate(zip(SCENARIOS, ("#175e9b", "#b14931"))):
        points = " ".join(f"{point},{y(value):.2f}" for point, value in zip(x, medians[scene]))
        svg.append(f'<polyline points="{points}" fill="none" stroke="{color}" stroke-width="3"/>')
        for point, value in zip(x, medians[scene]):
            svg.append(f'<circle cx="{point}" cy="{y(value):.2f}" r="5" fill="{color}"/>')
        samples = sum(record["scene"] == scene for record in records)
        svg.append(f'<text x="{72 + i * 252}" y="316" fill="{color}">{scene} · {samples} runs</text>')
    svg.extend(['<text x="72" y="342" fill="#5a6577">Mock measurements on one local host. See metadata.json for settings.</text>', '</g></svg>'])
    path.write_text("\n".join(svg) + "\n", encoding="utf-8")


def write_results(output, metadata, records):
    output.mkdir(parents=True, exist_ok=False)
    (output / "metadata.json").write_text(json.dumps(metadata, indent=2) + "\n", encoding="utf-8")
    runs = [{key: val for key, val in record.items() if key != "samples"} for record in records]
    (output / "reports.json").write_text(json.dumps(runs, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    with (output / "samples.csv").open("w", encoding="utf-8", newline="") as file:
        fields = ("round", "scene", "run_id", "stage", "started_at", "latency_ms", "error_code")
        writer = csv.DictWriter(file, fieldnames=fields, lineterminator="\n")
        writer.writeheader()
        for record in records:
            for sample in record["samples"]:
                writer.writerow({"round": record["round"], "scene": record["scene"],
                                 "run_id": record["report"]["run_id"], **sample})
    with (output / "stages.csv").open("w", encoding="utf-8", newline="") as file:
        fields = ("stage", "concurrency", "request_count", "completed_count", "error_count",
                  "p95_e2e_ms", "error_rate", "throughput_rps", "duration_seconds", "slo_status")
        writer = csv.DictWriter(file, fieldnames=("round", "scene", "run_id", "max_rps") + fields, lineterminator="\n")
        writer.writeheader()
        for record in records:
            report = record["report"]
            for stage in report["stage_summaries"]:
                writer.writerow({"round": record["round"], "scene": record["scene"], "run_id": report["run_id"],
                                 "max_rps": report["config"]["workload"]["max_rps"],
                                 **{key: stage[key] for key in fields}})
    write_chart(output / "p95-by-stage.svg", records)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, type=Path, help="new output directory")
    parser.add_argument("--repeats", type=int, choices=range(1, 4), default=3, help="runs per scene (default: 3)")
    parser.add_argument("--stage-seconds", type=int, default=30, help="sending window of each stage, 1 to 30 seconds (default: 30)")
    args = parser.parse_args()
    if not 1 <= args.stage_seconds <= 30:
        parser.error("--stage-seconds must be between 1 and 30")
    output = args.output.expanduser().resolve()
    if output.exists():
        parser.error("--output must be a new directory")
    try:
        metadata = {
            "experiment_type": "mock",
            "started_at": datetime.now(timezone.utc).isoformat(),
            "environment": {
                "os": platform.system(), "release": platform.release(), "arch": platform.machine(),
                "python": platform.python_version(), "go": read_command("go", "version"),
            },
            "build": {"git_commit": read_command("git", "rev-parse", "HEAD"),
                      "working_tree_dirty": bool(read_command("git", "status", "--porcelain"))},
            "sending_window_seconds": args.stage_seconds,
            "scenarios": {scene: {"delay_ms": delay, "repeats": args.repeats} for scene, (delay, _) in SCENARIOS.items()},
        }
        with tempfile.TemporaryDirectory(prefix="inferlab-thesis-") as tmp:
            directory = Path(tmp)
            binary = directory / "inferlab"
            database = directory / "experiment.db"
            subprocess.run(["go", "build", "-o", str(binary), "."], cwd=ROOT / "services/api", check=True)
            metadata["build"]["binary_sha256"] = hashlib.sha256(binary.read_bytes()).hexdigest()
            with start_api(binary, database) as api:
                records = run_experiments(api, database, args)
        metadata["finished_at"] = datetime.now(timezone.utc).isoformat()
        write_results(output, metadata, records)
    except (OSError, RuntimeError, ValueError, KeyError, http.client.HTTPException,
            sqlite3.Error, subprocess.CalledProcessError) as exc:
        parser.exit(1, f"Benchmark failed: {exc}\n")
    print(f"Wrote verified results to {output}")


if __name__ == "__main__":
    main()
