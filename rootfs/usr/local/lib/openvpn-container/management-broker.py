#!/usr/bin/env python3
"""Single-owner proxy for the OpenVPN management interface."""

from __future__ import annotations

import argparse
import os
import signal
import socket
import threading
import time
from dataclasses import dataclass, field


class RotatingLog:
    def __init__(self, path: str, max_bytes: int, backups: int) -> None:
        self.path = path
        self.max_bytes = max_bytes
        self.backups = backups
        self.lock = threading.Lock()
        directory = os.path.dirname(path)
        if directory:
            os.makedirs(directory, mode=0o750, exist_ok=True)
            os.chmod(directory, 0o750)

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
    lines: list[str] = field(default_factory=list)
    done: threading.Event = field(default_factory=threading.Event)
    error: str | None = None

    def add(self, line: str) -> None:
        self.lines.append(line)
        if line == "END":
            self.done.set()
        elif line.startswith(("SUCCESS:", "ERROR:")):
            self.done.set()


class OpenVPNBackend:
    def __init__(self, path: str, raw_log: RotatingLog, timeout: float) -> None:
        self.path = path
        self.raw_log = raw_log
        self.timeout = timeout
        self.command_lock = threading.Lock()
        self.state_lock = threading.Lock()
        self.connection_condition = threading.Condition(self.state_lock)
        self.connection: socket.socket | None = None
        self.initialization_generation = 0
        self.reload_in_progress = False
        self.pending: PendingResponse | None = None

    def _record_async(self, line: str) -> None:
        self.raw_log.write(line)
        if line.endswith(",Initialization Sequence Completed"):
            with self.connection_condition:
                self.initialization_generation += 1
                self.connection_condition.notify_all()

    def _disconnect(self, connection: socket.socket, error: str) -> None:
        with self.connection_condition:
            if self.connection is not connection:
                return
            self.connection = None
            pending = self.pending
            self.pending = None
            self.connection_condition.notify_all()
        try:
            connection.close()
        except OSError:
            pass
        if pending is not None and not pending.done.is_set():
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
        try:
            connection.settimeout(self.timeout)
            connection.connect(self.path)
            greeting = b""
            while not greeting.endswith(b"\n"):
                chunk = connection.recv(4096)
                if not chunk:
                    raise ConnectionError(
                        "OpenVPN management greeting is unavailable"
                    )
                greeting += chunk
                if len(greeting) > 65536:
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
                raise ConnectionError("OpenVPN log subscription was rejected")
            return connection
        except Exception as exc:
            with self.state_lock:
                owned = self.connection is connection
            if owned:
                self._disconnect(connection, str(exc))
            else:
                try:
                    connection.close()
                except OSError:
                    pass
            raise

    def _exchange(self, connection: socket.socket, command: str) -> list[str]:
        pending = PendingResponse()
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
        while True:
            self.wait_for_reload()
            with self.command_lock:
                with self.state_lock:
                    if self.reload_in_progress:
                        continue
                    connection = self.connection
                if connection is None:
                    connection = self._connect()
                return self._exchange(connection, command)

    def request_then_wait_for_initialization(self, command: str) -> list[str]:
        while True:
            self.wait_for_reload()
            with self.command_lock:
                with self.connection_condition:
                    if self.reload_in_progress:
                        continue
                    connection = self.connection
                if connection is None:
                    connection = self._connect()
                with self.connection_condition:
                    self.reload_in_progress = True
                    generation = self.initialization_generation
                try:
                    response = self._exchange(connection, command)
                except (OSError, ConnectionError, TimeoutError):
                    with self.connection_condition:
                        self.reload_in_progress = False
                        self.connection_condition.notify_all()
                    raise
            try:
                if response and response[0].startswith("SUCCESS:"):
                    self.wait_for_initialization(generation)
                return response
            finally:
                with self.connection_condition:
                    self.reload_in_progress = False
                    self.connection_condition.notify_all()

    def wait_for_reload(self) -> None:
        with self.connection_condition:
            while self.reload_in_progress:
                self.connection_condition.wait()

    def wait_for_initialization(self, previous_generation: int) -> None:
        deadline = time.monotonic() + self.timeout
        with self.connection_condition:
            while self.initialization_generation <= previous_generation:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise TimeoutError(
                        "OpenVPN did not complete initialization after reload"
                    )
                self.connection_condition.wait(remaining)

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
                        elif command == "signal SIGHUP":
                            lines = self.backend.request_then_wait_for_initialization(
                                command
                            )
                        else:
                            lines = self.backend.request(command)
                    except (OSError, ConnectionError, TimeoutError) as exc:
                        lines = [f"ERROR: management backend unavailable: {exc}"]
                    client.sendall(("\n".join(lines) + "\n").encode())
        except UnicodeError:
            try:
                client.sendall(b"ERROR: invalid UTF-8 management command\n")
            except OSError:
                pass
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
    def stop(_signum: int, _frame: object) -> None:
        broker.close()

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    signal.signal(signal.SIGHUP, signal.SIG_IGN)
    try:
        broker.serve()
    finally:
        broker.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
