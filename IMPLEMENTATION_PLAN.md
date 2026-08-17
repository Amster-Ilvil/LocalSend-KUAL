# v0.1.8 stability policy

v0.1.8 treats the validated v0.1.7 dual-platform transfer core as frozen.

## Frozen behavior

1. Preserve the Windows v2.1 fixed-Content-Length receive path.
2. Preserve the v2.2 streaming framing normalization path used by macOS.
3. Preserve LocalSend HTTP port 53317 and direct `/mnt/us/documents` receive behavior.
4. Preserve runtime-only firewall management.
5. Preserve current LocalSend identity and HTTPS/mTLS outbound semantics.

## Allowed hardening work

- daemon lifecycle and single-instance protection;
- crash recovery and stale temporary-file cleanup;
- logging bounds and diagnostics;
- storage checks;
- peer-state write throttling;
- regression tests and frozen-core integrity checks.

Any future protocol-core change should intentionally update `FROZEN_CORE_SHA256.txt` and repeat the cross-platform validation matrix before release.
