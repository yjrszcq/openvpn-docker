#!/usr/bin/env python3
"""Read and follow persistent structured OpenVPN container events."""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import time
from collections import deque
from pathlib import Path
from typing import Any, TextIO

UUID_PATTERN = re.compile(
    r"[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}"
)


class UsageParser(argparse.ArgumentParser):
    def error(self, message: str) -> None:
        self.print_usage(sys.stderr)
        print(f"ovpn: {message}", file=sys.stderr)
        raise SystemExit(64)


def parse_event(line: str) -> dict[str, Any]:
    event = json.loads(line)
    if not isinstance(event, dict):
        raise ValueError("event is not an object")
    required = ("timestamp", "event", "operation", "outcome")
    if any(not isinstance(event.get(field), str) or not event[field] for field in required):
        raise ValueError("event is missing required string fields")
    for field in ("client_id", "client_name"):
        if event.get(field) is not None and not isinstance(event[field], str):
            raise ValueError(f"{field} is not a string or null")
    return event


def format_text(event: dict[str, Any], no_trunc: bool) -> str:
    client_id = event.get("client_id")
    client_name = event.get("client_name")
    display_id = client_id
    if client_id and not no_trunc and UUID_PATTERN.fullmatch(client_id):
        display_id = client_id.replace("-", "")[:12]
    if client_id and client_name:
        identity = f"{client_name} [{display_id}]"
    elif client_id:
        identity = display_id
    elif client_name:
        identity = client_name
    else:
        identity = "-"
    core = {"timestamp", "event", "operation", "outcome", "client_id", "client_name"}
    details = " ".join(
        f"{key}={json.dumps(event[key], ensure_ascii=False, separators=(',', ':'))}"
        for key in sorted(event.keys() - core)
    )
    line = (
        f"{event['timestamp']} {event['event']} "
        f"{event['operation']} {event['outcome']} {identity}"
    )
    return f"{line} {details}" if details else line


def emit(line: str, json_output: bool, no_trunc: bool) -> None:
    try:
        event = parse_event(line)
    except (json.JSONDecodeError, ValueError) as exc:
        raise ValueError(f"invalid structured event: {exc}") from exc
    output = (
        json.dumps(event, ensure_ascii=False, separators=(",", ":"))
        if json_output
        else format_text(event, no_trunc)
    )
    try:
        print(output, flush=True)
    except BrokenPipeError:
        sys.stdout = open(os.devnull, "w", encoding="utf-8")
        raise SystemExit(0) from None


def history(event_file: Path, count: int) -> tuple[deque[str], TextIO | None]:
    lines: deque[str] = deque(maxlen=count if count > 0 else None)
    try:
        stream = event_file.open(encoding="utf-8")
    except FileNotFoundError:
        return lines, None
    if count == 0:
        stream.seek(0, os.SEEK_END)
        return lines, stream
    for line in stream:
        lines.append(line.rstrip("\r\n"))
    return lines, stream


def follow(
    event_file: Path,
    stream: TextIO | None,
    json_output: bool,
    no_trunc: bool,
) -> None:
    while True:
        if stream is None:
            try:
                stream = event_file.open(encoding="utf-8")
            except FileNotFoundError:
                time.sleep(0.2)
                continue
        line = stream.readline()
        if line:
            emit(line.rstrip("\r\n"), json_output, no_trunc)
            continue
        try:
            current_state = event_file.stat()
            stream_state = os.fstat(stream.fileno())
        except FileNotFoundError:
            time.sleep(0.2)
            continue
        if current_state.st_ino != stream_state.st_ino:
            stream.close()
            stream = event_file.open(encoding="utf-8")
            continue
        if current_state.st_size < stream.tell():
            stream.seek(0)
            continue
        time.sleep(0.2)


def main() -> int:
    parser = UsageParser(
        prog="ovpn runtime events",
        usage="ovpn runtime events [--lines N] [--follow] [--json] [--no-trunc]",
    )
    parser.add_argument("--lines", type=int, default=100)
    parser.add_argument("--follow", action="store_true")
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--no-trunc", action="store_true")
    parser.add_argument("--event-file", required=True, help=argparse.SUPPRESS)
    args = parser.parse_args()
    if args.lines < 0:
        parser.error("--lines must be non-negative")

    event_file = Path(args.event_file)
    try:
        lines, stream = history(event_file, args.lines)
        for line in lines:
            emit(line, args.json, args.no_trunc)
        if args.follow:
            try:
                follow(event_file, stream, args.json, args.no_trunc)
            except KeyboardInterrupt:
                return 0
        elif stream is not None:
            stream.close()
    except ValueError as exc:
        print(f"ovpn: unable to read events: {exc}", file=sys.stderr)
        return 1
    except OSError as exc:
        print(f"ovpn: unable to read events: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
