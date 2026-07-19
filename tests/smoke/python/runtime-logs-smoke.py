#!/usr/bin/env python3
"""History, translation, rename, and rotation tests for runtime logs."""

from __future__ import annotations

import os
import select
import subprocess
import sys
import tempfile
import time
from pathlib import Path

CLIENT_ID = "11111111-1111-4111-8111-111111111111"
UNKNOWN_ID = "22222222-2222-4222-8222-222222222222"
CLIENT_SHORT_ID = CLIENT_ID.replace("-", "")[:12]


def write_registry(path: Path, name: str) -> None:
    temporary = path.with_suffix(".tmp")
    temporary.write_text(
        f"# id,name,state\n{CLIENT_ID},{name},active\n",
        encoding="utf-8",
    )
    os.replace(temporary, path)


def write_registry_in_place(path: Path, name: str) -> None:
    path.write_text(
        f"# id,name,state\n{CLIENT_ID},{name},active\n",
        encoding="utf-8",
    )


def read_follow_line(process: subprocess.Popen[str]) -> str:
    assert process.stdout is not None
    ready, _, _ = select.select([process.stdout], [], [], 5)
    if not ready:
        raise AssertionError("timed out waiting for followed log line")
    line = process.stdout.readline()
    if line == "":
        raise AssertionError("runtime logs follow exited unexpectedly")
    return line.rstrip("\r\n")


def run_reader(script: Path, raw_log: Path, registry: Path, *arguments: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [
            sys.executable,
            str(script),
            *arguments,
            "--log-file",
            str(raw_log),
            "--registry",
            str(registry),
        ],
        check=False,
        text=True,
        capture_output=True,
    )


def main() -> int:
    root = Path(__file__).resolve().parents[3]
    script = root / "rootfs/usr/local/lib/openvpn-container/runtime-logs.py"
    with tempfile.TemporaryDirectory() as temporary:
        work = Path(temporary)
        registry = work / "client-state.csv"
        raw_log = work / "openvpn.log"
        write_registry(registry, "laptop")
        raw_log.with_name("openvpn.log.2").write_text(
            ">LOG:1,N,oldest\n",
            encoding="utf-8",
        )
        raw_log.with_name("openvpn.log.1").write_text(
            f">LOG:2,N,connected {CLIENT_ID}\n",
            encoding="utf-8",
        )
        raw_log.write_text(
            f">LOG:3,N,unknown {UNKNOWN_ID}\n"
            f">LOG:4,N,known {CLIENT_ID}\n",
            encoding="utf-8",
        )

        translated = run_reader(
            script,
            raw_log,
            registry,
            "-l",
            "3",
        )
        if translated.returncode != 0:
            raise AssertionError(translated.stderr)
        expected = [
            f">LOG:2,N,connected laptop [{CLIENT_SHORT_ID}]",
            f">LOG:3,N,unknown {UNKNOWN_ID}",
            f">LOG:4,N,known laptop [{CLIENT_SHORT_ID}]",
        ]
        if translated.stdout.splitlines() != expected:
            raise AssertionError(f"unexpected translated history: {translated.stdout!r}")

        full = run_reader(script, raw_log, registry, "-l", "1", "-t")
        if full.stdout.strip() != f">LOG:4,N,known laptop [{CLIENT_ID}]":
            raise AssertionError("no-trunc mode did not preserve the known client UUID")

        raw = run_reader(
            script, raw_log, registry, "-l", "1", "-r", "-t"
        )
        if raw.stdout.strip() != f">LOG:4,N,known {CLIENT_ID}":
            raise AssertionError("raw mode changed the OpenVPN log line")

        invalid = run_reader(script, raw_log, registry, "-l", "-1")
        if invalid.returncode != 64 or "--lines must be non-negative" not in invalid.stderr:
            raise AssertionError("negative history length was not rejected")

        follower = subprocess.Popen(
            [
                sys.executable,
                str(script),
                "-l",
                "0",
                "-f",
                "--log-file",
                str(raw_log),
                "--registry",
                str(registry),
            ],
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        try:
            time.sleep(0.3)
            with raw_log.open("a", encoding="utf-8") as stream:
                stream.write(f">LOG:5,N,before rename {CLIENT_ID}\n")
                stream.flush()
            if read_follow_line(follower) != (
                f">LOG:5,N,before rename laptop [{CLIENT_SHORT_ID}]"
            ):
                raise AssertionError("follow did not translate the initial name")

            registry.write_text(
                f"# xx,name,state\n{CLIENT_ID},laptop,active\n",
                encoding="utf-8",
            )
            invalid_signature = registry.stat()
            with raw_log.open("a", encoding="utf-8") as stream:
                stream.write(f">LOG:6,N,during registry error {CLIENT_ID}\n")
                stream.flush()
            if read_follow_line(follower) != (
                f">LOG:6,N,during registry error laptop [{CLIENT_SHORT_ID}]"
            ):
                raise AssertionError("follow discarded the last valid identity mapping")

            write_registry_in_place(registry, "tablet")
            os.utime(
                registry,
                ns=(invalid_signature.st_atime_ns, invalid_signature.st_mtime_ns),
            )
            with raw_log.open("a", encoding="utf-8") as stream:
                stream.write(f">LOG:7,N,after registry recovery {CLIENT_ID}\n")
                stream.flush()
            if read_follow_line(follower) != (
                f">LOG:7,N,after registry recovery tablet [{CLIENT_SHORT_ID}]"
            ):
                raise AssertionError("follow did not retry a failed registry signature")

            write_registry(registry, "workstation")
            os.replace(raw_log, raw_log.with_name("openvpn.log.1"))
            raw_log.write_text(
                f">LOG:8,N,after rename {CLIENT_ID}\n",
                encoding="utf-8",
            )
            if read_follow_line(follower) != (
                f">LOG:8,N,after rename workstation [{CLIENT_SHORT_ID}]"
            ):
                raise AssertionError("follow did not refresh mapping across rotation")
        finally:
            follower.terminate()
            follower.wait(timeout=3)

    print("runtime logs smoke passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
