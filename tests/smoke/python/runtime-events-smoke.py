#!/usr/bin/env python3
"""History, formatting, validation, and follow tests for runtime events."""

from __future__ import annotations

import json
import os
import select
import subprocess
import sys
import tempfile
import time
from pathlib import Path

CLIENT_ID = "11111111-1111-4111-8111-111111111111"
CLIENT_SHORT_ID = CLIENT_ID.replace("-", "")[:12]


def event(sequence: int, name: str = "laptop") -> dict[str, object]:
    return {
        "timestamp": f"2026-01-01T00:00:{sequence:02d}Z",
        "event": "client_connection",
        "operation": "connect",
        "outcome": "applied",
        "client_id": CLIENT_ID,
        "client_name": name,
        "sequence": sequence,
    }


def append_event(path: Path, value: dict[str, object]) -> None:
    with path.open("a", encoding="utf-8") as stream:
        stream.write(json.dumps(value, separators=(",", ":")) + "\n")
        stream.flush()


def run_reader(script: Path, event_file: Path, *arguments: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, str(script), *arguments, "--event-file", str(event_file)],
        check=False,
        text=True,
        capture_output=True,
    )


def read_follow_line(process: subprocess.Popen[str]) -> str:
    assert process.stdout is not None
    ready, _, _ = select.select([process.stdout], [], [], 5)
    if not ready:
        raise AssertionError("timed out waiting for followed event")
    line = process.stdout.readline()
    if not line:
        raise AssertionError("runtime events follow exited unexpectedly")
    return line.rstrip("\r\n")


def read_follow_error(process: subprocess.Popen[str]) -> str:
    assert process.stderr is not None
    ready, _, _ = select.select([process.stderr], [], [], 5)
    if not ready:
        raise AssertionError("timed out waiting for followed event warning")
    line = process.stderr.readline()
    if not line:
        raise AssertionError("runtime events follow exited without a warning")
    return line.rstrip("\r\n")


def main() -> int:
    root = Path(__file__).resolve().parents[3]
    script = root / "rootfs/usr/local/lib/openvpn-container/runtime-events.py"
    with tempfile.TemporaryDirectory() as temporary:
        event_file = Path(temporary) / "events.jsonl"
        for sequence in range(1, 4):
            append_event(event_file, event(sequence))

        text = run_reader(script, event_file, "-l", "1")
        expected = (
            f"2026-01-01T00:00:03Z client_connection connect applied "
            f"laptop [{CLIENT_SHORT_ID}] sequence=3"
        )
        if text.returncode != 0 or text.stdout.strip() != expected:
            raise AssertionError(f"unexpected text event output: {text!r}")

        full_text = run_reader(script, event_file, "-l", "1", "-t")
        expected_full = (
            f"2026-01-01T00:00:03Z client_connection connect applied "
            f"laptop [{CLIENT_ID}] sequence=3"
        )
        if full_text.returncode != 0 or full_text.stdout.strip() != expected_full:
            raise AssertionError(f"unexpected no-trunc event output: {full_text!r}")

        structured = run_reader(
            script, event_file, "-l", "2", "-j", "-t"
        )
        parsed = [json.loads(line) for line in structured.stdout.splitlines()]
        if structured.returncode != 0 or parsed != [event(2), event(3)]:
            raise AssertionError("JSON history did not preserve structured events")

        invalid_count = run_reader(script, event_file, "-l", "-1")
        if invalid_count.returncode != 64:
            raise AssertionError("negative event history length was accepted")

        malformed = Path(temporary) / "malformed.jsonl"
        malformed.write_text('{"event":"incomplete"}\n', encoding="utf-8")
        rejected = run_reader(script, malformed)
        if rejected.returncode != 1 or "invalid structured event" not in rejected.stderr:
            raise AssertionError("malformed structured event was accepted")

        follower = subprocess.Popen(
            [
                sys.executable,
                str(script),
                "-l",
                "0",
                "-f",
                "-j",
                "--event-file",
                str(event_file),
            ],
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        try:
            time.sleep(0.3)
            append_event(event_file, event(4))
            if json.loads(read_follow_line(follower)) != event(4):
                raise AssertionError("follow missed an appended event")

            with event_file.open("a", encoding="utf-8") as stream:
                stream.write("{bad json}\n")
                stream.flush()
            warning = read_follow_error(follower)
            if "invalid structured event" not in warning:
                raise AssertionError(f"unexpected malformed-event warning: {warning!r}")
            append_event(event_file, event(5))
            if json.loads(read_follow_line(follower)) != event(5):
                raise AssertionError("follow stopped after a malformed event")

            replacement = event_file.with_suffix(".new")
            append_event(replacement, event(6, "workstation"))
            os.replace(replacement, event_file)
            if json.loads(read_follow_line(follower)) != event(6, "workstation"):
                raise AssertionError("follow missed an atomically replaced event file")
        finally:
            follower.terminate()
            follower.wait(timeout=3)

    print("runtime events smoke passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
