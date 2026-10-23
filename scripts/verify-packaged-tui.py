#!/usr/bin/env python3
"""Run the installed CLI and real terminal frontend, with no source cwd.

Requires a disposable PostgreSQL database via DATABASE_URL or explicit
--local-database-bin for an owned temporary local cluster. Does not call a
model provider. The packaged executable must discover its own frontend/Node.
"""
import argparse
import fcntl
import os
from pathlib import Path
import pty
import re
import select
import signal
import struct
import subprocess
import tempfile
import termios
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--local-database-bin", type=Path, help="Installed PostgreSQL bin directory; owns a temporary cluster without DATABASE_URL")
    args = parser.parse_args()
    binary = args.binary.resolve(strict=True)
    external_url = os.environ.get("DATABASE_URL")
    if bool(external_url) == bool(args.local_database_bin):
        parser.error("Choose either DATABASE_URL for a disposable database or --local-database-bin")
    local_bin = args.local_database_bin.resolve(strict=True) if args.local_database_bin else None
    with tempfile.TemporaryDirectory(prefix="overkill-installed-tui-") as temporary:
        root = Path(temporary)
        workspace = root / "Unrelated Workspace"
        workspace.mkdir()
        env = {"LANG": "C.UTF-8"}
        if external_url:
            env["DATABASE_URL"] = external_url
        env.update(HOME=str(root), OVERKILL_HOME=str(root / "profile"),
                   TERM="xterm-256color", CI="false")
        # No source override and no system Node fallback: the installed copy
        # must carry Node adjacent to the executable or frontend resources.
        env["PATH"] = str(binary.parent)
        subprocess.run([str(binary), "config", "init"], cwd=workspace,
                       env=env, check=True, stdout=subprocess.PIPE,
                       stderr=subprocess.STDOUT, timeout=20)
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 50, 140, 0, 0))
        command = [str(binary), "tui"]
        if local_bin:
            command.extend(["--local-database", "--local-database-bin", str(local_bin)])
        process = subprocess.Popen(command, cwd=workspace, env=env,
                                   stdin=slave, stdout=slave, stderr=slave,
                                   start_new_session=True)
        os.close(slave)
        output = bytearray()
        accepted = False

        def wait_for(text, timeout=35):
            deadline = time.monotonic() + timeout
            start = len(output)
            while time.monotonic() < deadline:
                rendered = re.sub(rb"\x1b\[[0-?]*[ -/]*[@-~]", b"", output[start:])
                if text in rendered:
                    return
                if process.poll() is not None:
                    raise RuntimeError(f"installed TUI exited {process.returncode} before {text!r}")
                readable, _, _ = select.select([master], [], [], .2)
                if readable:
                    try:
                        output.extend(os.read(master, 65536))
                    except OSError as error:
                        raise RuntimeError("installed TUI closed its terminal unexpectedly") from error
            raise RuntimeError(f"installed TUI did not render {text!r}")

        def settle():
            # Drain complete redraws and allow React input effects to update
            # before the next key. PTYs have small buffers on macOS.
            deadline = time.monotonic() + .15
            while time.monotonic() < deadline:
                readable, _, _ = select.select([master], [], [], .02)
                if readable:
                    output.extend(os.read(master, 65536))

        try:
            # StatusBar emits this only after BackendClient.health succeeds
            # with the fresh per-launch bearer token supplied by the Go API.
            wait_for(b"/status")
            settle()
            os.write(master, b"\x0b")
            wait_for(b"Commands", 10)
            settle()
            query = bytearray()
            for character in b"Keyboard":
                query.append(character)
                os.write(master, bytes([character]))
                wait_for(b"> " + query, 10)
                settle()
            os.write(master, b"\r")
            wait_for(b"Toggle sidebar", 10)
            settle()
            os.write(master, b"\x1b")
            settle()
            os.write(master, b"\x03")
            # Drain while exiting, so a full PTY cannot deadlock shutdown.
            deadline = time.monotonic() + 10
            while process.poll() is None and time.monotonic() < deadline:
                readable, _, _ = select.select([master], [], [], .1)
                if readable:
                    try:
                        output.extend(os.read(master, 65536))
                    except OSError:
                        break
            code = process.wait(timeout=2)
            if code != 0:
                raise RuntimeError(f"installed TUI did not exit cleanly: {code}")
            try:
                os.killpg(process.pid, 0)
            except ProcessLookupError:
                pass
            else:
                raise RuntimeError("installed TUI left a child process running")
            if local_bin:
                cluster = root / "profile" / "postgres"
                if not (cluster / "PG_VERSION").is_file():
                    raise RuntimeError("owned local database did not persist its initialized cluster")
                if (cluster / "postmaster.pid").exists():
                    raise RuntimeError("owned local PostgreSQL remained running after TUI exit")
            accepted = True
        except Exception:
            # No inherited secrets are printed; only terminal output from this
            # fresh profile, which has no model credentials or user content.
            print(output[-8000:].decode("utf-8", errors="replace"))
            raise
        finally:
            if not accepted:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
            if process.poll() is None:
                process.wait(timeout=5)
            os.close(master)
            if not accepted and local_bin:
                cluster = root / "profile" / "postgres"
                if (cluster / "postmaster.pid").exists():
                    # This exact temporary cluster belongs to this fixture;
                    # never stop an external or caller-selected database.
                    subprocess.run([str(local_bin / "pg_ctl"), "stop", "-D", str(cluster),
                                    "-m", "fast", "-w", "-t", "10"],
                                   stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                   check=True, timeout=15)
    print("Packaged TUI authenticated startup, keyboard help and clean exit PASS")
    if local_bin:
        print("Fresh-profile owned PostgreSQL bootstrap, persisted cluster and shutdown PASS")


if __name__ == "__main__":
    main()
