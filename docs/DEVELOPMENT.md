# Development and contribution guide

## Architecture

Tuigram is a native Go program with a transport-independent application boundary:

```text
cmd/tuigram       CLI, config, authentication lifecycle, terminal startup
internal/core     Small domain structs and context-aware Client interface
internal/telegram Native Telegram API adapter, peer lookup, auth, media bounds
internal/demo     Thread-safe in-memory backend for demos and functional tests
internal/tui      Bubble Tea state machine, rendering, keyboard actions
internal/config   Defaults, JSON validation, environment overrides
internal/storage  Encrypted session, private filesystem access, bounded cache
```

The UI depends on `core.Client`, not generated Telegram types. Network/cache calls execute as Bubble Tea commands outside the update loop. Commands return messages with request identifiers; results from an earlier chat, query, or image selection are ignored. The transport owns its connection context, and cancellation propagates into the UI. Histories are chronological and bounded. The demo exercises the same interface without creating a Telegram session.

## Libraries

| Library | Purpose |
| --- | --- |
| [gotd/td](https://github.com/gotd/td) | Native Go MTProto transport, generated Telegram API, phone/2FA/QR login |
| [Bubble Tea](https://github.com/charmbracelet/bubbletea) | Terminal lifecycle and message/update/command model |
| [Lip Gloss](https://github.com/charmbracelet/lipgloss) | Terminal layout and theme styles |
| [qrterminal](https://github.com/mdp/qrterminal) | Locally rendered login QR codes |
| [x/crypto](https://pkg.go.dev/golang.org/x/crypto/scrypt) | scrypt key derivation |
| [x/term](https://pkg.go.dev/golang.org/x/term) | Terminal detection and masked password input |
| [x/sys](https://pkg.go.dev/golang.org/x/sys/unix) | Portable Unix process locking |

The Go standard library supplies AES-GCM, randomness, image decoding, JSON, synchronization, filesystem confinement, and tests. `go.mod` and `go.sum` pin dependencies. Go 1.26+ is required. `CGO_ENABLED=0` binaries support the release matrix without TDLib or system shared libraries.

## Everyday workflow

```sh
make demo
make fmt
make test
make vet
make race
make smoke
make vuln
```

The generated Telegram schema is large; the first compile and cross-compiles can take a while and require several gigabytes of RAM/cache. On small machines use `GOFLAGS=-p=1 GOGC=50` to limit concurrent compilation and compiler heap growth. Do not commit binaries, personal config, cache, or session data.

## Code and naming conventions

- Follow idiomatic Go and `gofmt`. Package names are short lowercase nouns; files use `snake_case.go`, tests `_test.go`.
- Export only the API another package needs. Use `MixedCaps` for identifiers and consistent initialisms (`ID`, `URL`, `API`). Avoid generic `util` packages.
- Keep Telegram-specific types inside `internal/telegram`. New capabilities first update the domain contract, then the real adapter, demo, and UI.
- Accept `context.Context` as the first argument for blocking operations. Honor cancellation, bound external reads/results, and keep network/disk work out of the UI update loop.
- Wrap errors with actionable context using `%w`. Never attach credentials, full RPC dumps, QR tokens, or message contents to errors/logs.
- Synchronize shared backend state. Return independent snapshots instead of mutable internal slices. Validate remote content before rendering or allocating large buffers.
- Use secure random identifiers for Telegram mutations. Do not automatically retry a failed send with a fresh identifier; it may duplicate a message after an ambiguous network failure.
- Preserve drafts on a failed send. Make additions discoverable in `?` help, update this README's key table, and expose errors inside the UI.
- Keep changes focused and describe the visible before/after behavior in PRs. Include relevant validation and remaining limitations. Conventional commit prefixes such as `feat:`, `fix:`, and `docs:` are welcome but not mandatory.

## Tests

Unit tests cover configuration validation, encrypted-session round trips/tampering/permissions, cache eviction/TTL/path safety, API request construction, media size bounds, text sanitization, and UI state transitions. Telegram adapter tests use a fake RPC invoker with real generated request/response types. The account-free functional journey drives the UI against the demo backend. CLI smoke tests execute the built binary and isolate all data directories.

CI never needs a phone number, login code, API hash, or session secret. Add regression tests for behavioral bugs and trust boundaries; avoid tests that just repeat implementation details. Run the race detector when changing asynchronous code. Sanitization fuzzing can be run with:

```sh
go test ./internal/tui -run '^$' -fuzz Fuzz -fuzztime 10s
```

For a release, manually verify a dedicated test account: phone/code + 2FA, QR renewal + 2FA, reconnect with the encrypted session, private/group/channel reads, permitted writes, forwarding, reaction, contact discovery/addition, and small/oversized photo previews. Use test conversations you control. Also verify terminal restoration on cancellation and native BSD behavior. Automated test success does not establish these live results.

## Releases and packaging

The GitHub Actions workflows run tests, smoke checks, vulnerability scanning, and cross-compilation for Linux, FreeBSD, OpenBSD, and Darwin on amd64/arm64. Native Linux/macOS jobs exercise executables; BSD artifacts require additional native qualification.

Release tags follow `vMAJOR.MINOR.PATCH`. The release workflow builds compressed binaries, includes the license and documentation, generates SHA-256 checksums, and creates a **draft** release. Review it and complete live-account/native-OS checks before publishing. Build locally with `bash scripts/package.sh GOOS GOARCH VERSION`, for example `bash scripts/package.sh linux amd64 dev`. `make package` packages the host platform by default.

`Formula/tuigram.rb` provides a HEAD build through a custom Homebrew tap. This repository itself can be tapped with its explicit GitHub URL once the formula is published. Release artifacts and any generated versioned formula must use their actual checksums. Do not substitute a guessed checksum or claim a tap/release is published before it exists.
