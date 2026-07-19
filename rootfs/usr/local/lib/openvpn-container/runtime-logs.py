#!/usr/bin/env python3
"""Read, translate, and follow persistent OpenVPN management logs."""

from __future__ import annotations

import argparse
import os
import re
import sys
import time
from collections import deque
from pathlib import Path
from typing import TextIO

UUID_PATTERN = re.compile(
    r"[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}"
)


class UsageParser(argparse.ArgumentParser):
    def error(self, message: str) -> None:
        self.print_usage(sys.stderr)
        print(f"ovpn: {message}", file=sys.stderr)
        raise SystemExit(64)


class IdentityMap:
    def __init__(self, registry: Path) -> None:
        self.registry = registry
        self.signature: tuple[int, int, int] | None = None
        self.names: dict[str, str] = {}

    def _current_signature(self) -> tuple[int, int, int] | None:
        try:
            state = self.registry.stat()
        except FileNotFoundError:
            return None
        return state.st_ino, state.st_mtime_ns, state.st_size

    def refresh(self) -> None:
        signature = self._current_signature()
        if signature == self.signature:
            return
        names: dict[str, str] = {}
        try:
            with self.registry.open(encoding="utf-8") as stream:
                if stream.readline().rstrip("\r\n") != "# id,name,state":
                    raise ValueError("invalid identity registry header")
                for raw in stream:
                    line = raw.rstrip("\r\n")
                    fields = line.split(",")
                    if len(fields) != 3 or UUID_PATTERN.fullmatch(fields[0]) is None:
                        raise ValueError("invalid identity registry record")
                    names[fields[0]] = fields[1]
        except (OSError, UnicodeError, ValueError):
            names = {}
        self.names = names
        self.signature = signature

    def translate(self, line: str, no_trunc: bool) -> str:
        self.refresh()
        return UUID_PATTERN.sub(
            lambda match: (
                f"{self.names[match.group(0)]} "
                f"[{match.group(0) if no_trunc else match.group(0).replace('-', '')[:12]}]"
                if match.group(0) in self.names
                else match.group(0)
            ),
            line,
        )


def rotated_paths(raw_log: Path) -> list[Path]:
    rotations: list[tuple[int, Path]] = []
    for candidate in raw_log.parent.glob(f"{raw_log.name}.*"):
        suffix = candidate.name.removeprefix(f"{raw_log.name}.")
        if suffix.isdigit():
            rotations.append((int(suffix), candidate))
    rotations.sort(reverse=True)
    return [path for _, path in rotations] + [raw_log]


def emit(line: str, raw: bool, no_trunc: bool, identities: IdentityMap) -> None:
    output = line if raw else identities.translate(line, no_trunc)
    try:
        print(output, flush=True)
    except BrokenPipeError:
        sys.stdout = open(os.devnull, "w", encoding="utf-8")
        raise SystemExit(0) from None


def history(
    raw_log: Path,
    count: int,
) -> tuple[deque[str], TextIO | None]:
    lines: deque[str] = deque(maxlen=count if count > 0 else None)
    current: TextIO | None = None
    if count == 0:
        try:
            current = raw_log.open(encoding="utf-8", errors="replace")
            current.seek(0, os.SEEK_END)
        except FileNotFoundError:
            current = None
        return lines, current
    for path in rotated_paths(raw_log):
        try:
            stream = path.open(encoding="utf-8", errors="replace")
        except FileNotFoundError:
            continue
        if path == raw_log:
            current = stream
        try:
            for line in stream:
                lines.append(line.rstrip("\r\n"))
        finally:
            if path != raw_log:
                stream.close()
    return lines, current


def follow(
    raw_log: Path,
    stream: TextIO | None,
    raw: bool,
    no_trunc: bool,
    identities: IdentityMap,
) -> None:
    while True:
        if stream is None:
            try:
                stream = raw_log.open(encoding="utf-8", errors="replace")
            except FileNotFoundError:
                time.sleep(0.2)
                continue
        line = stream.readline()
        if line:
            emit(line.rstrip("\r\n"), raw, no_trunc, identities)
            continue
        try:
            current_state = raw_log.stat()
            stream_state = os.fstat(stream.fileno())
        except FileNotFoundError:
            time.sleep(0.2)
            continue
        if current_state.st_ino != stream_state.st_ino:
            stream.close()
            stream = raw_log.open(encoding="utf-8", errors="replace")
            continue
        if current_state.st_size < stream.tell():
            stream.seek(0)
            continue
        time.sleep(0.2)


def main() -> int:
    parser = UsageParser(
        prog="ovpn runtime logs",
        usage="ovpn runtime logs [--lines N] [--follow] [--raw] [--no-trunc]",
    )
    parser.add_argument("--lines", type=int, default=100)
    parser.add_argument("--follow", action="store_true")
    parser.add_argument("--raw", action="store_true")
    parser.add_argument("--no-trunc", action="store_true")
    parser.add_argument("--log-file", required=True, help=argparse.SUPPRESS)
    parser.add_argument("--registry", required=True, help=argparse.SUPPRESS)
    args = parser.parse_args()
    if args.lines < 0:
        parser.error("--lines must be non-negative")

    raw_log = Path(args.log_file)
    identities = IdentityMap(Path(args.registry))
    try:
        lines, stream = history(raw_log, args.lines)
        for line in lines:
            emit(line, args.raw, args.no_trunc, identities)
        if args.follow:
            try:
                follow(raw_log, stream, args.raw, args.no_trunc, identities)
            except KeyboardInterrupt:
                return 0
        elif stream is not None:
            stream.close()
    except OSError as exc:
        print(f"ovpn: unable to read OpenVPN logs: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
