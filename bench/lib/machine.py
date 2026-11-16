# Copyright 2025-2026 Dawood K. (Nurysso)
# SPDX-License-Identifier: Apache-2.0
"""Which machine a timing came from. Timings are only reused on the same fingerprint."""
import hashlib
import os
import platform
from pathlib import Path

from .common import H


def _read(path) -> str:
    try:
        return Path(path).read_text().strip()
    except OSError:
        return ""


def cpu_model() -> str:
    for line in _read("/proc/cpuinfo").splitlines():
        if line.startswith("model name"):
            return line.split(":", 1)[1].strip()
    return platform.processor() or "unknown"


def mem_gib() -> int:
    for line in _read("/proc/meminfo").splitlines():
        if line.startswith("MemTotal:"):
            return round(int(line.split()[1]) / (1 << 20))
    return 0


def mem_total_bytes() -> int:
    for line in _read("/proc/meminfo").splitlines():
        if line.startswith("MemTotal:"):
            return int(line.split()[1]) * 1024
    return 0


def gpus() -> list[str]:
    """PCI vendor:device ids of display controllers, e.g. ["1002:73df"]."""
    ids = []
    for card in sorted(Path("/sys/class/drm").glob("card[0-9]")):
        vendor, device = _read(card / "device/vendor"), _read(card / "device/device")
        if vendor and device:
            ids.append(f"{vendor.removeprefix('0x')}:{device.removeprefix('0x')}")
    return ids


def info() -> dict:
    cpufreq = Path("/sys/devices/system/cpu/cpu0/cpufreq")
    return {
        "cpu": cpu_model(),
        "logical_cpus": os.cpu_count(),
        "mem_gib": mem_gib(),
        "arch": platform.machine(),
        # Not part of the fingerprint: they change without the hardware changing.
        "info": {
            "kernel": platform.release(),
            "governor": _read(cpufreq / "scaling_governor") or None,
            "epp": _read(cpufreq / "energy_performance_preference") or None,
            "boost": _read("/sys/devices/system/cpu/cpufreq/boost") or None,
            "loadavg_start": os.getloadavg()[0],
        },
    }


def fingerprint(gpu: bool = False) -> str:
    mid = _read("/etc/machine-id")
    fields = {
        "machine": hashlib.sha256(mid.encode()).hexdigest()[:16] if mid else "",
        "cpu": cpu_model(), "logical_cpus": os.cpu_count(), "mem_gib": mem_gib(), "arch": platform.machine(),
        "gpu": gpus() if gpu else None,
        "salt": os.environ.get("EULIX_BENCH_MACHINE_SALT", ""),  # test hook: pretend to be another machine
    }
    return H(fields)[:16]
