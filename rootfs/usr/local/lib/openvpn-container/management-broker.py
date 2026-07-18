#!/usr/bin/env python3
"""Single-owner proxy for the OpenVPN management interface."""

from __future__ import annotations

import argparse
import os
import signal
import socket
import sys
import threading
from dataclasses import dataclass, field


class RotatingLog:
    def __init__(self, path: str, max_bytes: int, backups: int) -> None:
        self.path = path
        self.max_bytes = max_bytes
        self.backups = backups
        self.lock = threading.Lock()
        os.makedirs(os.path.dirname(path), mode=0o750, exist_ok=True)
        os.chmod(os.path.dirname(path), 0o750)

    def _rotate(self) -> None:
        if self.backups == 0:
            try:
                os.unlink(self.path)
            except FileNotFoundError:
                pass
            return
        for index in range(self.backups, 1, -1):
            source = f"{self.path}.{index - 1}"
            destination = f"{self.path}.{index}"
            try:
                os.replace(source, destination)
                os.chmod(destination, 0o600)
            except FileNotFoundError:
                pass
        try:
            os.replace(self.path, f"{self.path}.1")
            os.chmod(f"{self.path}.1", 0o600)
        except FileNotFoundError:
            pass

    def write(self, line: str) -> None:
        payload = f"{line}\n".encode()
        with self.lock:
            try:
                size = os.path.getsize(self.path)
            except FileNotFoundError:
                size = 0
            if size > 0 and size + len(payload) > self.max_bytes:
                self._rotate()
            descriptor = os.open(
                self.path,
                os.O_WRONLY | os.O_CREAT | os.O_APPEND,
                0o600,
            )
            try:
                remaining = memoryview(payload)
                while remaining:
                    written = os.write(descriptor, remaining)
                    if written == 0:
                        raise OSError("persistent OpenVPN log write returned zero bytes")
                    remaining = remaining[written:]
                os.fchmod(descriptor, 0o600)
            finally:
                os.close(descriptor)


@dataclass
class PendingResponse:
    command: str
    lines: list[str] = field(default_factory=list)
    done: threading.Event = field(default_factory=threading.Event)
    error: str | None = None

    def add(self, line: str) -> None:
        self.lines.append(line)
        if line == "END":
            self.done.set()
        elif self.command.startswith(("kill ", "signal ", "log ")) and line.startswith(
            ("SUCCESS:", "ERROR:")
        ):
            self.done.set()


class OpenVPNBackend:
    def __init__(self, path: str, raw_log: RotatingLog, timeout: float) -> None:
        self.path = path
        self.raw_log = raw_log
        self.timeout = timeout
        self.command_lock = threading.Lock()
        self.state_lock = threading.Lock()
        self.connection: socket.socket | None = None
        self.pending: PendingResponse | None = None

    def _record_async(self, line: str) -> None:
        self.raw_log.write(line)

    def _disconnect(self, connection: socket.socket, error: str) -> None:
        with self.state_lock:
            if self.connection is not connection:
                return
            self.connection = None
            pending = self.pending
            self.pending = None
        try:
            connection.close()
        except OSError:
            pass
        if pending is not None:
            pending.error = error
            pending.done.set()

    def _reader(self, connection: socket.socket) -> None:
        try:
            with connection.makefile("r", encoding="utf-8", newline="\n") as stream:
                while True:
                    raw = stream.readline()
                    if raw == "":
                        raise ConnectionError("OpenVPN management connection closed")
                    line = raw.rstrip("\r\n")
                    if line.startswith(">"):
                        self._record_async(line)
                        continue
                    with self.state_lock:
                        pending = self.pending
                    if pending is None:
                        self._record_async(f">ORPHAN:{line}")
                        continue
                    pending.add(line)
        except (OSError, ConnectionError) as exc:
            self._disconnect(connection, str(exc))

    def _connect(self) -> socket.socket:
        connection = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        connection.settimeout(self.timeout)
        connection.connect(self.path)
        greeting = b""
        while not greeting.endswith(b"\n"):
            chunk = connection.recv(4096)
            if not chunk:
                connection.close()
                raise ConnectionError("OpenVPN management greeting is unavailable")
            greeting += chunk
            if len(greeting) > 65536:
                connection.close()
                raise ConnectionError("OpenVPN management greeting is too large")
        connection.settimeout(None)
        with self.state_lock:
            self.connection = connection
        threading.Thread(
            target=self._reader,
            args=(connection,),
            name="openvpn-management-reader",
            daemon=True,
        ).start()
        response = self._exchange(connection, "log on all")
        if not response or not response[0].startswith("SUCCESS:"):
            self._disconnect(connection, "OpenVPN log subscription was rejected")
            raise ConnectionError("OpenVPN log subscription was rejected")
        return connection

    def _exchange(self, connection: socket.socket, command: str) -> list[str]:
        pending = PendingResponse(command)
        with self.state_lock:
            if self.connection is not connection:
                raise ConnectionError("OpenVPN management connection changed")
            self.pending = pending
        try:
            connection.sendall(f"{command}\n".encode())
        except OSError as exc:
            self._disconnect(connection, str(exc))
        if not pending.done.wait(self.timeout):
            self._disconnect(connection, "OpenVPN management response timed out")
            raise TimeoutError("OpenVPN management response timed out")
        with self.state_lock:
            if self.pending is pending:
                self.pending = None
        if pending.error is not None:
            raise ConnectionError(pending.error)
        return pending.lines

    def request(self, command: str) -> list[str]:
        with self.command_lock:
            with self.state_lock:
                connection = self.connection
            if connection is None:
                connection = self._connect()
            return self._exchange(connection, command)

    def ensure_connected(self) -> None:
        with self.command_lock:
            with self.state_lock:
                connection = self.connection
            if connection is None:
                self._connect()

    def close(self) -> None:
        with self.state_lock:
            connection = self.connection
        if connection is not None:
            self._disconnect(connection, "management broker stopped")


class ManagementBroker:
    def __init__(
        self,
        listen: str,
        backend: str,
        raw_log: str,
        max_bytes: int,
        backups: int,
        timeout: float,
    ) -> None:
        self.listen = listen
        self.backend = OpenVPNBackend(
            backend,
            RotatingLog(raw_log, max_bytes, backups),
            timeout,
        )
        self.server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.stopping = threading.Event()

    def _serve_client(self, client: socket.socket) -> None:
        try:
            client.sendall(b">INFO:OpenVPN Management Broker Version 1\n")
            with client.makefile("r", encoding="utf-8", newline="\n") as stream:
                for raw in stream:
                    command = raw.rstrip("\r\n")
                    if command == "quit":
                        return
                    if not command:
                        continue
                    try:
                        if command == "broker-health":
                            self.backend.request("version")
                            lines = ["SUCCESS: broker connected to OpenVPN"]
                        else:
                            lines = self.backend.request(command)
                    except (OSError, ConnectionError, TimeoutError) as exc:
                        lines = [f"ERROR: management backend unavailable: {exc}"]
                    client.sendall(("\n".join(lines) + "\n").encode())
        except (BrokenPipeError, ConnectionResetError, OSError):
            return
        finally:
            client.close()

    def serve(self) -> None:
        os.makedirs(os.path.dirname(self.listen), mode=0o750, exist_ok=True)
        try:
            os.unlink(self.listen)
        except FileNotFoundError:
            pass
        self.server.bind(self.listen)
        os.chmod(self.listen, 0o600)
        self.server.listen(32)
        self.server.settimeout(1.0)
        threading.Thread(
            target=self._maintain_backend,
            name="openvpn-management-connector",
            daemon=True,
        ).start()
        while not self.stopping.is_set():
            try:
                client, _ = self.server.accept()
            except socket.timeout:
                continue
            except OSError:
                break
            threading.Thread(
                target=self._serve_client,
                args=(client,),
                name="management-broker-client",
                daemon=True,
            ).start()

    def _maintain_backend(self) -> None:
        while not self.stopping.is_set():
            try:
                self.backend.ensure_connected()
            except (OSError, ConnectionError, TimeoutError):
                pass
            self.stopping.wait(0.2)

    def close(self) -> None:
        self.stopping.set()
        self.backend.close()
        self.server.close()
        try:
            os.unlink(self.listen)
        except FileNotFoundError:
            pass


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--listen", required=True)
    parser.add_argument("--backend", required=True)
    parser.add_argument("--raw-log", required=True)
    parser.add_argument("--max-bytes", type=int, required=True)
    parser.add_argument("--backups", type=int, required=True)
    parser.add_argument("--timeout", type=float, default=5.0)
    parser.add_argument("--reload-script")
    args = parser.parse_args()
    if args.max_bytes < 1:
        parser.error("--max-bytes must be positive")
    if args.backups < 0:
        parser.error("--backups must be non-negative")
    broker = ManagementBroker(
        args.listen,
        args.backend,
        args.raw_log,
        args.max_bytes,
        args.backups,
        args.timeout,
    )
    reload_requested = False

    def stop(_signum: int, _frame: object) -> None:
        broker.close()

    def reload_code(_signum: int, _frame: object) -> None:
        nonlocal reload_requested
        reload_requested = True
        broker.close()

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    signal.signal(signal.SIGHUP, reload_code)
    try:
        broker.serve()
    finally:
        broker.close()
    if reload_requested:
        if not args.reload_script:
            print("management broker: no reload script configured", file=sys.stderr)
            return 1
        os.execv(
            sys.executable,
            [sys.executable, args.reload_script, *sys.argv[1:]],
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
