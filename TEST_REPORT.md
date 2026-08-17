# LocalSend-KUAL v0.1.8 validation summary

The release keeps the validated v0.1.7 transfer core byte-for-byte frozen and adds only lifecycle/stability hardening around it.

## Release gates

- `go test ./...`: PASS
- `go vet ./...`: PASS
- targeted race tests for transfer/hardening paths: PASS
- frozen dual-platform core SHA-256 check: PASS
- Windows v2.1 fixed Content-Length receive: PASS
- v2.2 missing Content-Length + missing Transfer-Encoding + chunked stream: PASS
- v2.2 missing Content-Length + missing Transfer-Encoding + raw stream: PASS
- Windows → v2.2 stream → Windows → v2.2 stream cross-platform sequence: PASS
- byte-for-byte payload verification: PASS
- stale daemon lock recovery: PASS
- second-daemon rejection: PASS
- interrupted-transfer temporary-file cleanup: PASS
- normal EPUB preservation during cleanup: PASS
- bounded log rotation: PASS
- KUAL shell syntax: PASS
- ARMv7 build: ELF32 ARM EABI5, statically linked, no dynamic section: PASS

Hardware/client behavior can vary by Kindle firmware and LocalSend version; test new protocol-core changes on real devices before updating the frozen baseline.
