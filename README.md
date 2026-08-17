# LocalSend-KUAL v0.1.8 for Kindle Voyage

v0.1.8 is a stability-only wrapper release around the **frozen v0.1.7 dual-platform transfer core**.

The following behavior is intentionally unchanged from the version verified on the real Voyage:

- LocalSend v2.2 receiver over HTTP on port 53317.
- Temporary runtime-only wlan0 firewall lease; removed on stop.
- Receive directly into `/mnt/us/documents`.
- Windows v2.1 fixed-Content-Length upload path.
- macOS v2.2 missing-Content-Length / missing-Transfer-Encoding normalization.
- HTTPS/mTLS outbound client behavior and certificate fingerprint identity.
- Session/token/IP validation and existing transfer semantics.

## Frozen core

`FROZEN_CORE_SHA256.txt` records SHA-256 hashes of the eight source files that implement the successful v0.1.7 Windows/macOS transfer core. `go test ./...` includes `TestFrozenDualPlatformCoreV017`, which fails if any of those files change.

For this reason the internal core startup line still says `v0.1.7`; v0.1.8 adds an immediately preceding line identifying the stability wrapper.

## v0.1.8 hardening

- Separate singleton `daemon.lock` prevents two serve commands racing before `daemon.pid` is created.
- Stale daemon locks are recovered automatically; live LocalSend processes are never replaced.
- Startup removes only LocalSend-owned `.localsend-part` temporary files left by an interrupted transfer.
- Runtime log is capped at ~1 MiB with one backup (`localsend.log.1`).
- KUAL also rotates an oversized log before starting the binary.
- KUAL startup waits for the full self-test instead of assuming a fixed two-second startup is enough.
- If startup self-test fails, the daemon is stopped and temporary firewall rules are cleaned up.
- PID checks verify `/proc/<pid>/cmdline` contains `localsend-kindle`, reducing stale/reused PID false positives.
- Network diagnostics now include free storage and keep a 16 MiB safety threshold.
- Repeated identical peer announcements update LastSeen in memory without rewriting `peers.json` every 30 seconds. New/changed IP, protocol, port or metadata are persisted immediately; unchanged peers are checkpointed periodically.

## Install

1. In KUAL: `LocalSend -> Stop LocalSend`.
2. Extract the install ZIP to the Kindle USB root and overwrite the extension files.
3. Start `LocalSend -> Receive 10 minutes` or continuous receive.

The install ZIP deliberately does **not** contain `config/settings.json`, identity files, peer state, PID files or logs, so an upgrade preserves the already-working configuration and device identity.

## Receive directory

Files are saved directly under:

`/mnt/us/documents`

Kindle-supported book formats can therefore be indexed by the stock library.
