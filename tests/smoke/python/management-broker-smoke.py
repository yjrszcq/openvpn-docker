#!/usr/bin/env python3
"""Concurrency and reconnect smoke test for the management broker."""

from __future__ import annotations

import os
import socket
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path


class FakeOpenVPN:
    def __init__(self, path: Path) -> None:
        self.path = path
        self.server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.server.bind(str(path))
        self.server.listen(4)
        self.server.settimeout(0.2)
        self.stopping = threading.Event()
        self.accepted = 0
        self.active = 0
        self.max_active = 0
        self.lock = threading.Lock()
        self.thread = threading.Thread(target=self._serve, daemon=True)

    def start(self) -> None:
        self.thread.start()

    def _serve(self) -> None:
        while not self.stopping.is_set():
            try:
                connection, _ = self.server.accept()
            except socket.timeout:
                continue
            except OSError:
                return
            with self.lock:
                self.accepted += 1
                self.active += 1
                self.max_active = max(self.max_active, self.active)
            try:
                self._handle(connection)
            finally:
                connection.close()
                with self.lock:
                    self.active -= 1

    def _handle(self, connection: socket.socket) -> None:
        connection.sendall(b">INFO:OpenVPN Management Interface Version 5\n")
        with connection.makefile("r", encoding="utf-8") as stream:
            for raw in stream:
                command = raw.rstrip("\r\n")
                if command == "log on all":
                    connection.sendall(b"SUCCESS: real-time log notification set to ON\n")
                elif command == "version":
                    connection.sendall(
                        b"OpenVPN Version: OpenVPN 2.7.5 test\n"
                        b">LOG:1,N,async-version-line\n"
                        b"Management Version: 5\nEND\n"
                    )
                elif command == "status 3":
                    connection.sendall(
                        b"TITLE\tOpenVPN 2.7.5\n"
                        b">LOG:2,N,async-status-line\n"
                        b"GLOBAL_STATS\tMax bcast/mcast queue length\t0\nEND\n"
                    )
                elif command.startswith("kill "):
                    connection.sendall(b"SUCCESS: client killed\n")
                elif command == "signal SIGHUP":
                    connection.sendall(b"SUCCESS: signal SIGHUP thrown\n")
                    return
                else:
                    connection.sendall(b"ERROR: unsupported test command\n")

    def close(self) -> None:
        self.stopping.set()
        self.server.close()
        self.thread.join(timeout=2)


def request(path: Path, command: str) -> str:
    connection = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    connection.settimeout(5)
    connection.connect(str(path))
    with connection:
        stream = connection.makefile("r", encoding="utf-8")
        greeting = stream.readline()
        if not greeting.startswith(">INFO:OpenVPN Management Broker"):
            raise AssertionError(f"unexpected broker greeting: {greeting!r}")
        connection.sendall(f"{command}\n".encode())
        lines: list[str] = []
        while True:
            line = stream.readline()
            if line == "":
                break
            line = line.rstrip("\r\n")
            lines.append(line)
            if line == "END" or line.startswith(("SUCCESS:", "ERROR:")):
                break
        connection.sendall(b"quit\n")
    return "\n".join(lines)


def wait_for_socket(path: Path) -> None:
    for _ in range(100):
        if path.is_socket():
            return
        time.sleep(0.02)
    raise AssertionError(f"socket was not created: {path}")


def main() -> int:
    root = Path(__file__).resolve().parents[3]
    broker_script = (
        root / "rootfs/usr/local/lib/openvpn-container/management-broker.py"
    )
    with tempfile.TemporaryDirectory() as temporary:
        work = Path(temporary)
        backend_path = work / "openvpn.sock"
        broker_path = work / "broker.sock"
        raw_log = work / "openvpn.log"
        fake = FakeOpenVPN(backend_path)
        fake.start()
        broker = subprocess.Popen(
            [
                sys.executable,
                str(broker_script),
                "--listen",
                str(broker_path),
                "--backend",
                str(backend_path),
                "--raw-log",
                str(raw_log),
                "--max-bytes",
                "240",
                "--backups",
                "2",
                "--timeout",
                "2",
            ]
        )
        try:
            wait_for_socket(broker_path)
            for _ in range(100):
                with fake.lock:
                    if fake.accepted > 0:
                        break
                time.sleep(0.02)
            else:
                raise AssertionError("broker did not proactively connect to OpenVPN")
            outputs: list[str] = []
            errors: list[BaseException] = []

            def concurrent_request(command: str) -> None:
                try:
                    outputs.append(request(broker_path, command))
                except BaseException as exc:  # noqa: BLE001 - smoke aggregation
                    errors.append(exc)

            threads = [
                threading.Thread(
                    target=concurrent_request,
                    args=("status 3" if index % 2 else "version",),
                )
                for index in range(12)
            ]
            for thread in threads:
                thread.start()
            for thread in threads:
                thread.join()
            if errors:
                raise errors[0]
            if len(outputs) != 12:
                raise AssertionError("not every concurrent request completed")
            if any(">LOG:" in output for output in outputs):
                raise AssertionError("async OpenVPN lines leaked into command responses")
            if not any("GLOBAL_STATS" in output for output in outputs):
                raise AssertionError("status response was not proxied")
            if not any("OpenVPN Version:" in output for output in outputs):
                raise AssertionError("version response was not proxied")
            if not request(broker_path, "kill client-id").startswith("SUCCESS:"):
                raise AssertionError("kill response was not proxied")
            if not request(broker_path, "signal SIGHUP").startswith("SUCCESS:"):
                raise AssertionError("SIGHUP response was not proxied")
            for _ in range(100):
                try:
                    reconnected = request(broker_path, "broker-health")
                    if reconnected.startswith("SUCCESS:"):
                        break
                except (ConnectionError, OSError):
                    pass
                time.sleep(0.02)
            else:
                raise AssertionError("broker did not reconnect to OpenVPN")
            rotated_logs = [
                path
                for path in (raw_log.with_name("openvpn.log.2"), raw_log.with_name("openvpn.log.1"), raw_log)
                if path.exists()
            ]
            async_content = "".join(
                path.read_text(encoding="utf-8") for path in rotated_logs
            )
            if "async-version-line" not in async_content:
                raise AssertionError("async log lines were not separated")
            if len(rotated_logs) < 2:
                raise AssertionError("persistent OpenVPN log did not rotate")
            if any(path.stat().st_mode & 0o077 for path in rotated_logs):
                raise AssertionError("persistent OpenVPN logs are not private")
            if fake.max_active != 1 or fake.accepted < 2:
                raise AssertionError("broker did not retain single-owner reconnect semantics")
        finally:
            broker.terminate()
            try:
                broker.wait(timeout=3)
            except subprocess.TimeoutExpired:
                broker.kill()
                broker.wait(timeout=3)
            fake.close()
    print("management broker smoke passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
