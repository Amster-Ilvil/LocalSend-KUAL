#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Fail CI when git-tracked files look like private/local runtime data."""
from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path, PurePosixPath

ROOT = Path(__file__).resolve().parents[1]
SELF = "scripts/privacy_audit.py"
ALLOWED_ENV = {".env.example", ".env.sample", ".env.template"}
FORBIDDEN_NAMES = {
    ".env", ".env.local", ".env.production", ".env.development", ".env.test",
    "settings.json", "credentials.json", "secrets.json", "service-account.json",
    "device.crt", "device.key", "http-fingerprint", "peers.json", "status.json",
    "daemon.pid", "daemon.lock", "pending-install.json", "install.lock",
}
FORBIDDEN_DIRS = {"state", "logs", ".localsend-install-backup", ".localsend-install-stage"}
FORBIDDEN_SUFFIXES = {
    ".log", ".db", ".sqlite", ".sqlite3", ".pem", ".crt", ".key", ".p12",
    ".pfx", ".p8", ".der", ".jks", ".keystore", ".ovpn",
}
SECRET_PATTERNS = [
    ("private key", re.compile(r"-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----")),
    ("OpenAI-style key", re.compile(r"(?<![A-Za-z0-9])" + "sk" + r"-(?:proj-|svcacct-)?[A-Za-z0-9_-]{24,}")),
    ("GitHub token", re.compile(r"(?<![A-Za-z0-9])" + "github_pat_" + r"[A-Za-z0-9_]{20,}")),
    ("GitHub token", re.compile(r"(?<![A-Za-z0-9])" + "gh" + r"[pousr]_[A-Za-z0-9]{20,}")),
    ("AWS access key", re.compile(r"(?<![A-Z0-9])" + "AKIA" + r"[0-9A-Z]{16}(?![A-Z0-9])")),
    ("Google API key", re.compile(r"(?<![A-Za-z0-9])" + "AIza" + r"[0-9A-Za-z_-]{30,}")),
    ("Hugging Face token", re.compile(r"(?<![A-Za-z0-9])" + "hf_" + r"[A-Za-z0-9]{20,}")),
    ("Slack token", re.compile(r"(?<![A-Za-z0-9])" + "xox" + r"[baprs]-[A-Za-z0-9-]{20,}")),
    ("Stripe live secret", re.compile(r"(?<![A-Za-z0-9])" + "sk_live_" + r"[A-Za-z0-9]{20,}")),
    ("Bearer token", re.compile(r"(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{24,}\b")),
    ("credential URL", re.compile(r"https?://[^\s/:@]+:[^\s/@]+@[^\s/]+", re.I)),
]
MAC_HOME = re.compile(r"/Users/(?!Shared(?:/|$)|runner(?:/|$)|<|USER(?:/|$)|username(?:/|$)|yourname(?:/|$)|path(?:/|$))[^/\s\"']+")
LINUX_HOME = re.compile(r"/home/(?!runner(?:/|$)|<|USER(?:/|$)|username(?:/|$)|yourname(?:/|$)|path(?:/|$))[^/\s\"']+")
WIN_HOME = re.compile(r"(?i)[A-Z]:\\Users\\(?!Public\\|runneradmin\\|<|USER\\|username\\|yourname\\|path\\)[^\\\r\n\"']+")


def tracked_files() -> list[str]:
    proc = subprocess.run(
        ["git", "ls-files", "-z"], cwd=ROOT,
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    if proc.returncode != 0:
        raise SystemExit("privacy audit requires a git checkout")
    return [p.decode("utf-8", "surrogateescape") for p in proc.stdout.split(b"\0") if p]


def path_violations(paths: list[str]) -> list[str]:
    problems: list[str] = []
    for raw in paths:
        p = PurePosixPath(raw)
        name = p.name.lower()
        parts = {part.lower() for part in p.parts}
        if name.startswith(".env") and name not in ALLOWED_ENV:
            problems.append(f"forbidden tracked environment file: {raw}")
        if name in FORBIDDEN_NAMES:
            problems.append(f"forbidden tracked runtime/credential file: {raw}")
        if parts & FORBIDDEN_DIRS:
            problems.append(f"forbidden tracked runtime directory: {raw}")
        if p.suffix.lower() in FORBIDDEN_SUFFIXES:
            problems.append(f"forbidden tracked private/runtime file: {raw}")
        if name.startswith(".localsend-part") or ".localsend-install-" in name or ".localsend-rollback-" in name:
            problems.append(f"forbidden tracked transfer/install residue: {raw}")
    return problems


def text_violations(paths: list[str]) -> list[str]:
    problems: list[str] = []
    for raw in paths:
        if raw == SELF:
            continue
        path = ROOT / raw
        try:
            if not path.is_file() or path.stat().st_size > 10 * 1024 * 1024:
                continue
            data = path.read_bytes()
        except OSError:
            continue
        if b"\x00" in data[:8192]:
            continue
        text = data.decode("utf-8", "ignore")
        for label, pattern in SECRET_PATTERNS:
            if pattern.search(text):
                problems.append(f"possible {label} in tracked file: {raw}")
        for label, pattern in (("macOS home path", MAC_HOME), ("Linux home path", LINUX_HOME), ("Windows home path", WIN_HOME)):
            for match in pattern.finditer(text):
                start = text.rfind("\n", 0, match.start()) + 1
                end = text.find("\n", match.end())
                end = len(text) if end < 0 else end
                line = text[start:end].lower()
                if "re.compile" in line or "regex" in line or "pattern" in line:
                    continue
                problems.append(f"possible private {label} in tracked file: {raw}")
                break
    return problems


def main() -> int:
    paths = tracked_files()
    problems = sorted(set(path_violations(paths) + text_violations(paths)))
    if problems:
        print("PRIVACY AUDIT FAILED", file=sys.stderr)
        for item in problems:
            print(f"- {item}", file=sys.stderr)
        return 1
    print(f"Privacy audit passed: {len(paths)} tracked files checked; no blocked private/local artifacts detected.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
