"""Local built-artifact interoperability checks; Python standard library only."""
import argparse
import ctypes
from ctypes import wintypes
import json
import os
from pathlib import Path
import queue
import socket
import struct
import subprocess
import threading
import time
import zipfile

ROOT = Path(__file__).resolve().parent.parent
BUILD = ROOT / ".build"
JDK = ROOT / ".tools/java-me/jdk/jdk8u504-b01/bin"
EMULATOR = ROOT / ".tools/java-me/microemulator-2.0.4.jar"


def checked(args):
    return subprocess.check_output([str(a) for a in args], cwd=ROOT, stderr=subprocess.STDOUT).decode("utf-8", "replace")


def verify_java(codec=True):
    jar = BUILD / "java-me/ogm-first-contact/ogm-first-contact.jar"
    jad = jar.with_suffix(".jad").read_text("ascii")
    assert "MIDlet-Jar-Size: " + str(jar.stat().st_size) in jad
    names = []
    with zipfile.ZipFile(jar) as package:
        for name in package.namelist():
            if name.endswith(".class"):
                assert name.startswith("ogm/"), "Third-party class bundled"
                data = package.read(name)
                assert struct.unpack(">IHH", data[:8]) == (0xCAFEBABE, 3, 45)
                names.append(name[:-6].replace("/", "."))
    details = checked([JDK / "javap.exe", "-v", "-classpath", jar] + names)
    assert "StackMap:" in details, "CLDC preverification metadata missing"
    (BUILD / "java-me/class-inspection.txt").write_text(details, encoding="utf-8")
    if codec:
        tests = BUILD / "java-me/codec-test"
        tests.mkdir(exist_ok=True)
        checked([JDK / "javac.exe", "-cp", jar, "-d", tests, ROOT / "clients/java-me/test/CodecTest.java"])
        print(checked([JDK / "java.exe", "-cp", str(tests) + os.pathsep + str(jar), "CodecTest"]).strip())
    print("PASS: %d MIDlet classes at version 45.3; CLDC StackMap present; JAD size matches" % len(names))


def sample(pid):
    """Windows working set/private bytes and cumulative CPU, not a heap estimate."""
    class Counters(ctypes.Structure):
        _fields_ = [("cb", wintypes.DWORD), ("faults", wintypes.DWORD)] + [
            (name, ctypes.c_size_t) for name in ("peak_ws", "ws", "peak_paged", "paged", "peak_nonpaged", "nonpaged", "pagefile", "peak_pagefile", "private")]
    kernel = ctypes.WinDLL("kernel32", use_last_error=True)
    psapi = ctypes.WinDLL("psapi", use_last_error=True)
    kernel.OpenProcess.restype = wintypes.HANDLE
    kernel.OpenProcess.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD]
    kernel.CloseHandle.argtypes = [wintypes.HANDLE]
    kernel.GetProcessTimes.argtypes = [wintypes.HANDLE] + [ctypes.POINTER(wintypes.FILETIME)] * 4
    psapi.GetProcessMemoryInfo.argtypes = [wintypes.HANDLE, ctypes.POINTER(Counters), wintypes.DWORD]
    handle = kernel.OpenProcess(0x410, False, pid)
    if not handle:
        raise ctypes.WinError(ctypes.get_last_error())
    try:
        counters = Counters()
        counters.cb = ctypes.sizeof(counters)
        if not psapi.GetProcessMemoryInfo(handle, ctypes.byref(counters), counters.cb):
            raise ctypes.WinError(ctypes.get_last_error())
        times = [wintypes.FILETIME() for _ in range(4)]
        if not kernel.GetProcessTimes(handle, *[ctypes.byref(t) for t in times]):
            raise ctypes.WinError(ctypes.get_last_error())
        cpu = sum((t.dwHighDateTime << 32) | t.dwLowDateTime for t in times[2:]) / 10000000
        return {"working_set_bytes": counters.ws, "private_bytes": counters.private, "cpu_seconds": cpu}
    finally:
        kernel.CloseHandle(handle)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--idle-seconds", type=int, default=10)
    parser.add_argument("--ui", action="store_true", help="drive the packaged MIDlet controls in the headless emulator")
    parser.add_argument("--quick", action="store_true", help="UI reconnection check without idle, exhaustive codec or load tests; preserve full-run logs")
    args = parser.parse_args()
    if not 0 <= args.idle_seconds <= 600:
        parser.error("idle duration must be between 0 and 600 seconds")
    if args.quick:
        args.ui = True
        args.idle_seconds = 0
    output = BUILD / "short-check" if args.quick else BUILD
    output.mkdir(parents=True, exist_ok=True)
    verify_java(codec=not args.quick)
    events = queue.Queue()
    processes = []
    logs = {}

    def launch(name, command):
        process = subprocess.Popen([str(c) for c in command], cwd=ROOT, stdin=subprocess.PIPE,
                                   stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                   text=True, encoding="utf-8", errors="strict", bufsize=1)
        processes.append(process)
        logs[name] = []
        def reader():
            try:
                for line in process.stdout:
                    logs[name].append(line.rstrip())
                    events.put((name, line.rstrip()))
            finally:
                events.put((name, "<EOF>"))
        threading.Thread(target=reader, daemon=True).start()
        return process

    def wait_line(name, prefix, timeout=15):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            source, line = events.get(timeout=max(0.01, deadline - time.monotonic()))
            if source == name and line.startswith(prefix):
                return line
            if source == name and line == "<EOF>":
                raise RuntimeError("%s exited: %s" % (name, logs[name]))
        raise TimeoutError(prefix)

    def command(process, value):
        process.stdin.write(value + "\n")
        process.stdin.flush()

    result = {}
    try:
        server = launch("server", [BUILD / "ogm-server.exe", "-listen", "127.0.0.1:0"])
        address = wait_line("server", "", 10).split()[-1]
        assert address.startswith("127.0.0.1:"), address
        console = launch("console", [BUILD / "ogm-console.exe", "-server", address, "-id", "100001"])
        wait_line("console", "Connected as 100001")
        command(console, "/disconnect")
        wait_line("console", "Disconnected:")
        time.sleep(0.1)
        command(console, "/connect")
        wait_line("console", "Connected as 100001")
        artifact = "ogm-first-contact" if args.ui else "ogm-emulator-test"
        testdir = BUILD / "java-me" / artifact
        original = testdir / (artifact + ".jad")
        jad = original.read_text("ascii").replace("127.0.0.1:19090", address)
        jad = jad.rstrip() + "\nOGM-Test-Idle-Millis: %d\n" % (args.idle_seconds * 1000)
        runtime_jad = testdir / ("short-check.jad" if args.quick else "local-test.jad")
        runtime_jad.write_text(jad, encoding="ascii")
        cache = ROOT / ".cache/emulator"
        cache.mkdir(parents=True, exist_ok=True)
        emulator_command = [JDK / "java.exe", "-Dfile.encoding=UTF-8", "-Duser.home=" + str(cache), "-Djava.io.tmpdir=" + str(cache)]
        if args.ui:
            driver = BUILD / "java-me/ui-test"
            driver.mkdir(exist_ok=True)
            checked([JDK / "javac.exe", "-cp", EMULATOR, "-d", driver, ROOT / "clients/java-me/test/EmulatorUI.java"])
            emulator_command += ["-cp", str(driver) + os.pathsep + str(EMULATOR), "EmulatorUI", runtime_jad, str(args.idle_seconds * 1000)]
        else:
            emulator_command += ["-cp", EMULATOR, "org.microemu.app.Headless", "--quit", runtime_jad]
        emulator = launch("emulator", emulator_command)
        expected = []
        fixtures = ["ASCII", "\u041f\u0440\u0438\u0432\u0456\u0442", "Za\u017c\u00f3\u0142\u0107", "e\u0301", "\U0001f642"]
        for cycle in range(4):
            expected.extend("C%d-%d %s" % (cycle, i, fixtures[i % len(fixtures)]) for i in range(10))
            if cycle == 0:
                expected.append("AFTER-IDLE")
        count = 0
        payload_bytes = 0
        idle_start = None
        deadline = time.monotonic() + args.idle_seconds + 40
        while time.monotonic() < deadline:
            source, line = events.get(timeout=max(0.01, deadline - time.monotonic()))
            if source == "console" and line.startswith("From 100002: "):
                text = line[len("From 100002: "):]
                assert count < len(expected) and text == expected[count], repr((count, text))
                if count == 10:
                    result["idle_end"] = sample(server.pid)
                    result["idle_elapsed_seconds"] = round(time.monotonic() - idle_start, 3)
                command(console, "/send 100002 " + text)
                count += 1
                payload_bytes += len(text.encode("utf-8"))
                if count == 10:
                    result["idle_start"] = sample(server.pid)
                    idle_start = time.monotonic()
                    print("PASS: first 10 exact echoes; starting %d-second idle check" % args.idle_seconds, flush=True)
            if source == "emulator" and "OGM-EMULATOR-FAIL" in line:
                raise AssertionError(line)
            if source == "emulator" and ("Cannot load" in line or "Exception" in line):
                raise AssertionError("Emulator initialization/runtime failure: " + line)
            if source == "emulator" and "OGM-EMULATOR-PASS" in line:
                assert count == 41
                break
            if source in ("server", "console", "emulator") and line == "<EOF>":
                raise RuntimeError(source + " exited early")
        else:
            raise TimeoutError("Emulator exchange")
        assert emulator.wait(timeout=10) == 0
        result["echoes"] = count
        # Each echo is four frames crossing the relay: two SENDs and two MESSAGEs.
        result["exchange_application_bytes_all_directions"] = 4 * (payload_bytes + 7 * count)
        result["after_exchange"] = sample(server.pid)
        command(console, "/quit")
        assert console.wait(timeout=5) == 0
        if args.quick:
            result["status"] = "PASS"
            print(json.dumps(result, indent=2))
            return
        time.sleep(0.1)
        # Two direct test peers isolate relay resource use from emulator overhead.
        def frame(kind, identity, text=""):
            body = struct.pack(">BI", kind, identity) + text.encode("utf-8")
            return struct.pack(">H", len(body)) + body
        def read_frame(peer):
            def exact(n):
                data = b""
                while len(data) < n:
                    part = peer.recv(n - len(data))
                    if not part:
                        raise EOFError("Relay closed during measurement")
                    data += part
                return data
            length = struct.unpack(">H", exact(2))[0]
            assert 2 <= length <= 2005
            return exact(length)
        host, port = address.split(":")
        with socket.create_connection((host, int(port)), timeout=5) as first, socket.create_connection((host, int(port)), timeout=5) as second:
            for peer, identity in ((first, 100001), (second, 100002)):
                peer.sendall(frame(1, identity))
                assert read_frame(peer) == struct.pack(">BI", 129, identity)
            payload = "\U0001f642" * 500
            to_second, to_first = frame(2, 100002, payload), frame(2, 100001, payload)
            expected_first, expected_second = frame(130, 100001, payload)[2:], frame(130, 100002, payload)[2:]
            result["busy_start"] = sample(server.pid)
            started = time.monotonic()
            for _ in range(10000):
                first.sendall(to_second)
                assert read_frame(second) == expected_first
                second.sendall(to_first)
                assert read_frame(first) == expected_second
            result["busy_elapsed_seconds"] = round(time.monotonic() - started, 3)
            result["busy_end"] = sample(server.pid)
            result["busy_routed_messages"] = 20000
            result["busy_application_bytes_all_directions"] = 10000 * 4 * 2007
        result["status"] = "PASS"
        print(json.dumps(result, indent=2))
    finally:
        for process in reversed(processes):
            if process.poll() is None:
                process.terminate()
            process.wait(timeout=10)
        (output / "local-test-result.json").write_text(json.dumps(result, indent=2), encoding="utf-8")
        for name, lines in logs.items():
            (output / (name + "-test.log")).write_text("\n".join(lines), encoding="utf-8")


if __name__ == "__main__":
    main()
