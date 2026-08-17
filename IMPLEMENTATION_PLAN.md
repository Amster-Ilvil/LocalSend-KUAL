# v0.1.8 stability-hardening plan

## Non-negotiable freeze

Treat the real-device-successful v0.1.7 transfer implementation as immutable. The frozen files are listed in `FROZEN_CORE_SHA256.txt` and guarded by an automated SHA-256 test.

No changes are permitted in this release to:

- Windows v2.1 fixed-length upload behavior.
- macOS v2.2 framing normalizer.
- LocalSend endpoint semantics.
- HTTP port 53317 compatibility mode.
- temporary firewall lease implementation.
- outbound HTTPS/mTLS identity and certificate validation.

## Allowed hardening layer

Changes are limited to process lifecycle, log lifecycle, crash residue cleanup, diagnostics and state persistence write reduction.

## Release gates

- Frozen core SHA-256 check.
- Full Go tests and `go vet`.
- Targeted race tests for dual-platform and hardening paths.
- Final executable cross-platform sequence: Windows -> macOS chunked -> Windows -> macOS raw.
- Byte-for-byte and SHA-256 comparison for every received file.
- Real second-daemon rejection and stale partial cleanup smoke test.
- KUAL shell syntax and JSON validation.
- ARMv7 ELF32 / EABI5 / statically linked / no dynamic section verification.
- Install archive audit: no settings, identities, peer state, PID or logs.
