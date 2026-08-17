# LocalSend-KUAL v0.1.8 test report

v0.1.8 is a stability wrapper over the frozen v0.1.7 transfer core.

## Frozen core integrity

`sha256sum -c FROZEN_CORE_SHA256.txt`: PASS

Frozen files:

- internal/app/server.go
- internal/app/upload_compat.go
- internal/app/client.go
- internal/app/discovery.go
- internal/app/firewall.go
- internal/app/identity.go
- internal/app/http_identity.go
- internal/app/types.go

## Automated validation

- `go test ./...`: PASS
- `go vet ./...`: PASS
- targeted `go test -race` covering dual-platform compatibility, mTLS, daemon lock, partial cleanup, log rotation and state throttling: PASS
- `sh -n extension/localsend/bin/kual.sh`: PASS
- `python3 -m json.tool extension/localsend/menu.json`: PASS

## Final executable cross-platform smoke

One final compiled daemon process received, in order:

1. Windows v2.1 fixed Content-Length: 67,848 bytes — byte compare PASS.
2. macOS v2.2 missing CL/TE, actual chunked stream: 134,804 bytes — byte compare PASS.
3. Windows v2.1 fixed Content-Length: 178,017 bytes — byte compare PASS.
4. macOS v2.2 missing CL/TE, raw stream: 313,177 bytes — byte compare PASS.

All four returned HTTP 200 and matched expected SHA-256 values.

Result: `CROSS_PLATFORM_SMOKE=PASS`.

## Hardening smoke

- First daemon starts normally: PASS.
- Second daemon is rejected with a non-zero exit code: PASS.
- Stale `.localsend-part` file is removed on startup: PASS.
- Normal EPUB beside it is preserved: PASS.
- `daemon.lock` is released after clean stop: PASS.

Result: `HARDENING_SMOKE=PASS`.

## ARM build

- Go: 1.23.x toolchain used by this environment.
- GOOS=linux
- GOARCH=arm
- GOARM=7
- CGO_ENABLED=0
- ELF32 little-endian ARM
- EABI5
- statically linked
- no dynamic section
