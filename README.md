# LocalSend-KUAL

A lightweight, LocalSend-compatible KUAL extension for **Kindle Voyage** and other compatible jailbroken Kindle devices.

> **Status:** v0.1.8 stable hardening release. The dual-platform transfer core from v0.1.7 is intentionally frozen because it has been validated with both Windows and macOS LocalSend clients.

## What it does

LocalSend-KUAL runs a small Go daemon on the Kindle and exposes the LocalSend v2 API over the local network. It is designed for old Kindle hardware where the official Flutter desktop/mobile application is not a practical fit.

- Receive files from LocalSend on **Windows** and **macOS**.
- Save received files directly to `/mnt/us/documents/` so supported Kindle document formats can be indexed by the stock system.
- Send files from `/mnt/us/LocalSend/Outbox` to discovered LocalSend peers.
- KUAL controls for start, timed receive, stop, discovery, send and diagnostics.
- Runtime-only firewall allowance for LocalSend traffic; rules are removed on shutdown.
- Static ARMv7 Linux binary; no runtime package manager is required.
- Checksum verification, safe relative paths and temporary-file/atomic-rename receive flow.

## Compatibility model

The compatibility layer deliberately keeps the two proven receive paths separate:

- **Windows / LocalSend v2.1-style uploads:** standard HTTP requests with a positive `Content-Length` use the original receive path unchanged.
- **LocalSend v2.2 streaming uploads:** clients that omit both `Content-Length` and `Transfer-Encoding` are normalized *before* Go `net/http` parsing, then passed to the same receive handler.

The frozen core is recorded in [`FROZEN_CORE_SHA256.txt`](FROZEN_CORE_SHA256.txt), and automated tests fail if those files are unintentionally changed.

## v0.1.8 hardening

v0.1.8 adds stability around the already-working transfer core rather than changing protocol behavior:

- single-instance daemon lock with stale-lock recovery;
- stale `.localsend-part*` cleanup after interrupted transfers;
- bounded log rotation;
- stricter PID/process validation;
- KUAL startup readiness checks;
- storage-space diagnostics;
- peer-state write throttling to reduce unnecessary flash writes;
- regression tests that lock the validated v0.1.7 Windows/macOS core.

## Requirements

- Jailbroken Kindle with KUAL.
- Kindle Voyage is the primary tested target.
- Wi-Fi LAN shared with the sending/receiving LocalSend device.
- Root privileges available to the KUAL extension for the temporary firewall rule.

## Build from source

Go 1.23.x is recommended for the current old-kernel compatibility target.

```bash
go test ./...
go vet ./...

CGO_ENABLED=0 \
GOOS=linux \
GOARCH=arm \
GOARM=7 \
go build -trimpath -ldflags='-s -w' \
  -o extension/localsend/bin/localsend-kindle \
  ./cmd/localsend-kindle
```

The resulting binary should be an ELF32 ARM EABI5 executable and statically linked.

## Install

1. Stop any running LocalSend-KUAL instance from KUAL.
2. Build the ARMv7 binary and place it at:

   ```text
   /mnt/us/extensions/localsend/bin/localsend-kindle
   ```

3. Copy the `extension/localsend` directory to:

   ```text
   /mnt/us/extensions/localsend
   ```

4. Ensure `bin/kual.sh` and `bin/localsend-kindle` are executable.
5. Open KUAL → **LocalSend** → start receiving.

The default receive directory is:

```text
/mnt/us/documents
```

The default outbox is:

```text
/mnt/us/LocalSend/Outbox
```

## Runtime files and privacy

Runtime identity and device state are generated **on the Kindle** and are intentionally excluded from this repository. Do not commit them.

Ignored/private runtime data includes:

```text
extension/localsend/state/
extension/localsend/logs/
*.crt
*.key
peers.json
status.json
daemon.pid
daemon.lock
```

The repository contains no device certificates, private keys, LocalSend fingerprints, peer IP history, transfer logs, account credentials or user files.

## Network and security notes

- The default Kindle receive service uses LocalSend-compatible LAN HTTP on port `53317` for compatibility with the validated Voyage setup.
- The KUAL launcher creates a temporary firewall rule only for the LocalSend port/interface and removes it on shutdown.
- HTTPS/mTLS remains supported for outbound connections to peers that advertise HTTPS.
- Treat LocalSend-KUAL as a trusted-LAN utility. Do not expose port `53317` directly to the public Internet.

## Repository layout

```text
cmd/localsend-kindle/       CLI entry point
internal/app/               protocol, discovery, receive/send and hardening logic
extension/localsend/        KUAL menu, launcher and default configuration
FROZEN_CORE_SHA256.txt      validated transfer-core hashes
TEST_REPORT.md              release validation summary
```

## Project relationship

This is an independent compatibility implementation for Kindle/KUAL. It is **not an official LocalSend project** and is not affiliated with the LocalSend maintainers. LocalSend names and protocol references are used only to describe interoperability.

## License

MIT License. See [`LICENSE`](LICENSE).
