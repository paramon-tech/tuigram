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
internal/platform Cancellable media playback and duplex microphone/speaker audio
```

The UI depends on `core.Client` and optional history/read-state, organization, media, attachment upload, chat/contact management, and native call interfaces, not generated Telegram types. Network, file, and playback operations execute as Bubble Tea commands outside the update loop. Commands return messages with request identifiers; results from an earlier chat, query, or image selection are ignored. The transport owns its connection context, and cancellation propagates into the UI. Histories are chronological and bounded. The demo exercises the same interfaces without creating a Telegram session. It retains uploaded media in memory and simulates call controls without opening audio devices.

Attachment downloads stream into private partial files and publish only completed files under generated safe names. The 512 MiB attachment bound is independent of the 8 MiB preview bound. Playback uses a separate temporary directory and a cancellable `exec.CommandContext` process with no shell or terminal input/output. Playback functions can be injected for account-free tests.

`core.AttachmentClient` sends JPEG/PNG photos, MP4/MOV videos, or generic documents; `core.BatchAttachmentClient` publishes albums of up to 10 files and 512 MiB aggregate. `core.OpenAttachment` validates a regular local file and keeps inspection and upload on the same handle. Photos are capped at 10 MiB, videos at 512 MiB, and captions at 1,024 characters. MP4 metadata parsing uses bounded seeks to identify the video track, dimensions, duration, and streaming layout. The Telegram adapter streams through gotd's uploader and uses `messages.uploadMedia` to prepare grouped media, then publishes `messages.sendMedia` or one `messages.sendMultiMedia` only after complete, unchanged input has been verified. Mixed document/media groups are rejected before upload unless all entries use send-as-files. The final mutation is never automatically retried. The form captures its destination, preserves failed input, and supports cancellation independently of call state.

`core.HistoryClient` provides exclusive message-ID cursors and earliest/latest windows for history and server-side search. Raw message IDs advance cursors even through service-only pages. The UI merges pages by ID, retains selection, and rejects stale chat/query/generation responses. Explicit mutations refresh the selected historical window. `core.ReadClient` acknowledges only the viewed unfiltered boundary and returns an authoritative dialog snapshot; newer arrivals and hidden views are protected. Read completions invalidate older in-flight dialog refreshes.

`core.OrganizationClient` changes Telegram pin/archive/mute preferences and ordinary folder membership. Dialog loading separates pinned and unpinned pagination for Inbox/Archive, includes older explicit folder members, and honors inherited notification settings. The TUI retains canonical dialogs separately from the filtered list. Passive unread filtering keeps the currently read conversation selected to avoid automatically draining successive unread chats.

The settings panel uses `config.Preferences`, which contains no API credentials. Saves reload the existing private config without environment overrides, update only preferences, and use atomic private-file replacement. Shortcut validation preserves fixed navigation, call and quit controls. Poll timers use generations so saving a new interval cannot leave duplicate polling loops. Demo preferences remain in memory.

`core.CallClient` exposes one native two-way voice call at a time. Incoming calls and signaling arrive through Telegram updates; a synchronized backend owns call identity, lifecycle, and cleanup. The UI reads its state every 300 ms without polling Telegram for calls. The pinned gotd [WebRTC call transport](https://gotd.dev/docs/advanced/calls/) handles negotiation and network media. Its custom Telegram reflector relay support is incomplete, so some network/peer combinations cannot connect. Live call interoperability remains unverified.

Microphone capture starts after explicit start/answer and transport connection. FFmpeg captures the selected audio device and encodes Opus/RTP to an ephemeral localhost UDP socket. The backend writes packets to the call's audio track; incoming Opus packets are repackaged into Ogg and piped to ffplay. No call recordings are written to disk. Mute gates transmitted packets while keeping capture open. Bounded queues prevent a slow player from blocking network delivery; hangup and shutdown stop and reap both processes. Audio factories and call drivers can be substituted in tests. Speaker selection follows the OS output device; echo cancellation is not implemented.

## Libraries

| Library | Purpose |
| --- | --- |
| [gotd/td](https://github.com/gotd/td) | Native Go MTProto transport, generated Telegram API, phone/2FA/QR login |
| [Pion WebRTC](https://github.com/pion/webrtc) | Go WebRTC media transport used by gotd calls |
| [Bubble Tea](https://github.com/charmbracelet/bubbletea) | Terminal lifecycle and message/update/command model |
| [Lip Gloss](https://github.com/charmbracelet/lipgloss) | Terminal layout and theme styles |
| [qrterminal](https://github.com/mdp/qrterminal) | Locally rendered login QR codes |
| [x/crypto](https://pkg.go.dev/golang.org/x/crypto/scrypt) | scrypt key derivation |
| [x/term](https://pkg.go.dev/golang.org/x/term) | Terminal detection and masked password input |
| [x/sys](https://pkg.go.dev/golang.org/x/sys/unix) | Portable Unix process locking |

The Go standard library supplies AES-GCM, randomness, image decoding, JSON, synchronization, filesystem confinement, and tests. `go.mod` and `go.sum` pin dependencies. Go 1.26+ is required. `CGO_ENABLED=0` builds the tuigram binary without TDLib or linked system audio libraries. Native calls additionally need installed FFmpeg/libopus and ffplay executables with the selected capture backend and audio output support.

`call_input_format` and `call_input_device` configure the microphone; `--call-input-format` and `--call-input-device` override them. Empty format selects AVFoundation on macOS, PulseAudio on Linux, OSS on FreeBSD, or sndio on OpenBSD. Linux can select ALSA explicitly. `tuigram audio check` checks the tools without opening devices; `tuigram audio devices` enumerates inputs and may trigger an OS permission prompt. Neither command establishes a Telegram call.

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

Unit tests cover configuration validation, encrypted-session round trips/tampering/permissions, cache eviction/TTL/path safety, API request construction, media size bounds, text sanitization, and UI state transitions. Telegram adapter tests use a fake RPC invoker with real generated request/response types. The account-free functional journey drives the UI against the demo backend, including physical spaces in text and forms. CLI smoke tests execute the built binary and isolate all data directories.

Upload tests cover real photo/video fixtures, bounded MP4 parsing, sparse large-file streaming, cancellation, changed files, failed upload/send responses, preserved captions, and captured destinations. Demo tests check preview/download/forward round trips. UI tests cover empty captions, failed-form retention, duplicate-send prevention, batch controls, reaction replacement/removal, and successful history refresh. Pagination/read tests cover sparse IDs, single-item pages, earliest history/search, stale requests, service messages, unseen arrivals, narrow panes, and call overlays. Organization journeys cover archived forward destinations, folder selection, unread filtering, contact rule changes, and keeping picker identity across refreshes. Preference tests verify saving does not persist environment credentials, key conflicts are rejected, and failed saves do not change active settings. The small video fixture is a locally generated blue H.264 clip and does not require FFmpeg to run these tests.

Call tests exercise incoming/outgoing state transitions, explicit microphone startup, mute/hangup, stale events, cancellation, and cleanup using substituted call/audio drivers. Audio tests cover RTP bounds and Ogg framing. An opt-in local integration test uses installed FFmpeg/ffplay with a generated tone and a dummy output driver; it opens neither a real microphone nor speakers and does not connect to Telegram:

```sh
TUIGRAM_TEST_CALL_AUDIO=1 go test -race ./internal/platform -run TestCallAudioSyntheticLoopback -count=1
```

CI never needs a phone number, login code, API hash, or session secret. Add regression tests for behavioral bugs and trust boundaries; avoid tests that just repeat implementation details. Run the race detector when changing asynchronous code. Sanitization fuzzing can be run with:

```sh
go test ./internal/tui -run '^$' -fuzz Fuzz -fuzztime 10s
```

For a release, manually verify a dedicated test account: phone/code + 2FA, QR renewal + 2FA, reconnect with the encrypted session, private/group/channel reads, permitted writes, forwarding, reaction, contact discovery/addition, and small/oversized photo previews. Use test conversations you control. Send a JPEG/PNG and an MP4/MOV with a caption, verify the received media, cancel an upload, and check the chat before retrying an ambiguous send.

Qualify native calls with a consenting second test account on another device: check incoming/outgoing setup, microphone permission denial and recovery, two-way audible speech, device selection, mute/unmute, decline, remote/local hangup, disconnect, and quitting during setup or an active call. Confirm the microphone indicator stops after hangup. Test across distinct networks to expose relay compatibility limits. Verify terminal restoration and native BSD behavior separately. Mocked RPC tests, synthetic audio loopback, and `audio check` do not establish successful live Telegram calls or actual hardware audio.

## Releases and packaging

The GitHub Actions workflows run tests, smoke checks, vulnerability scanning, and cross-compilation for Linux, FreeBSD, OpenBSD, and Darwin on amd64/arm64. Native Linux/macOS jobs exercise executables; BSD artifacts require additional native qualification.

Release tags follow `vMAJOR.MINOR.PATCH`. The release workflow builds compressed binaries, includes the license and documentation, generates SHA-256 checksums, and creates a **draft** release. Review it and complete live-account/native-OS checks before publishing. Build locally with `bash scripts/package.sh GOOS GOARCH VERSION`, for example `bash scripts/package.sh linux amd64 dev`. `make package` packages the host platform by default.

`Formula/tuigram.rb` provides a HEAD build through a custom Homebrew tap. This repository itself can be tapped with its explicit GitHub URL once the formula is published. Release artifacts and any generated versioned formula must use their actual checksums. Do not substitute a guessed checksum or claim a tap/release is published before it exists.
