# tuigram

Telegram, at keyboard speed. A native Go terminal client for private conversations, groups, and channels, with vim-style navigation and a small local footprint.

**Early development:** the native Telegram integration is implemented and covered by mocked RPC tests. Live account authentication and messaging still need manual verification. The demo and functional tests run without credentials.

```sh
go build -o bin/tuigram ./cmd/tuigram
./bin/tuigram --demo
```

Requires Go 1.26 or newer to build. No TDLib, C compiler, bot token, or database is required. Press `?` inside the app for help; `q` quits. A headless preview is available with `--demo --snapshot`.

## Features

| Feature | Available behavior |
| --- | --- |
| Conversations | Browse private chats, groups, supergroups, and channels; unread counts; periodic refresh |
| Messaging | Read and send text, multiline composition, per-chat in-memory drafts |
| Forwarding | Forward a selected message to another conversation |
| Reactions | Display standard emoji reactions and counts; send a 👍 reaction |
| Rich content | Unicode emoji, visible URL destinations, forwarded indicators, attachment labels |
| Images | Download on demand and preview PNG/JPEG/GIF with terminal color blocks; first frame for GIF |
| Search | Search message text in the current chat; find existing contacts and public usernames |
| Contacts | Open a discovered person's chat or add them to Telegram contacts |
| Login | Phone/code, Telegram two-step verification, or a renewing QR code |
| Navigation | Vim-style movement, pane switching, scrollable message reader, keyboard-only dialogs |
| Themes | Midnight, light, and Dracula |
| Local storage | Encrypted login session, bounded media cache, configurable TTL and size |

## Install

Build from a checkout:

```sh
git clone https://github.com/paramon-tech/tuigram.git
cd tuigram
make build
./bin/tuigram --demo
```

The build matrix targets Linux, FreeBSD, OpenBSD, and macOS on **amd64 and arm64**. Tagged release automation prepares compressed binaries, checksums, and a draft GitHub release. No release is claimed to exist yet. BSD binaries are cross-compiled; native BSD runtime verification is still pending.

The repository includes a Homebrew formula. After these files are published to GitHub, install the development version through the custom tap:

```sh
brew tap paramon-tech/tuigram https://github.com/paramon-tech/tuigram
brew install --HEAD paramon-tech/tuigram/tuigram
```

A formula fits a command-line tool; no macOS application cask is needed. Release packaging also prepares the versioned installation assets; see [packaging](docs/DEVELOPMENT.md#releases-and-packaging).

## Connect to Telegram

Create your own application credentials at [my.telegram.org](https://my.telegram.org), as described in [Telegram's API guide](https://core.telegram.org/api/obtaining_api_id). Tuigram uses the user-account API, not the Bot API.

```sh
export TUIGRAM_API_ID='your-numeric-api-id'
export TUIGRAM_API_HASH='your-32-character-api-hash'
./bin/tuigram
# Or scan from Telegram → Settings → Devices → Link Desktop Device:
./bin/tuigram --qr
```

At first launch, choose a **local session passphrase** of at least 12 characters, with at least four distinct non-space characters. Tuigram uses this to encrypt your saved login. This passphrase is separate from your Telegram 2FA password. You will need it again on later launches. Password input is hidden, and the passphrase is never saved to disk.

Phone login asks for your international phone number and the login code delivered by Telegram. Both phone and QR login support Telegram's two-step verification password when required. Existing sessions reconnect without another code. Account creation must be done in an official Telegram app.

For a managed local environment, `TUIGRAM_SESSION_PASSPHRASE` can supply the session passphrase. Prefer the interactive prompt on shared machines; environment variables can be exposed by process inspection or diagnostics. Never put credentials into a tracked file.

## Keys

| Key | Action |
| --- | --- |
| `j` / `k`, arrows | Move through conversations or messages |
| `h` / `l`, `Tab` | Switch panes |
| `gg` / `G`, `Ctrl+u` / `Ctrl+d` | First/last item; move by a page |
| `Enter` | Open conversation or selected picker item |
| `i` | Compose; `Enter` inserts a newline |
| `Ctrl+s` | Send composed text |
| `/` | Search current conversation; `Esc` clears search |
| `c` | Search contacts/public usernames |
| `a` in contact picker | Add selected person to Telegram contacts |
| `f` | Pick a destination and forward selected message |
| `r` | React 👍 to selected message |
| `v` | Preview selected image |
| `y` | Read full message with scrolling |
| `t` | Cycle theme for the current session |
| `R` | Refresh |
| `?`, `Esc`, `q` / `Ctrl+c` | Help, close/cancel, quit |

The layout switches to a single pane in narrow terminals. Emoji appearance depends on your terminal/font; true color gives the best image previews. URLs are displayed as text and never opened automatically. The composer currently supports append, backspace, newline, and `Ctrl+u` to clear.

## Configuration and cache

```sh
./bin/tuigram config init
./bin/tuigram cache stats
./bin/tuigram cache clear
./bin/tuigram --theme dracula
./bin/tuigram --config /absolute/private/path/config.json
```

Flags precede commands. `config init` prints the path and creates a private JSON config without copying credentials from the environment. Edit that file to persist settings. Partial JSON objects inherit defaults; unknown fields are rejected.

```json
{
  "theme": "midnight",
  "cache_max_bytes": 33554432,
  "cache_ttl_hours": 24,
  "poll_seconds": 5
}
```

| Setting | Default / behavior |
| --- | --- |
| `app_id`, `app_hash` | Unset; `TUIGRAM_API_ID` / `TUIGRAM_API_HASH` override config |
| `theme` | `midnight`, `light`, or `dracula` |
| `cache_max_bytes` | 32 MiB; `0` disables caching and prunes existing cache entries |
| `cache_ttl_hours` | 24 hours; expired entries are removed during cache access/startup |
| `cache_dir` | OS cache directory + `tuigram`; `XDG_CACHE_HOME` overrides base |
| `state_dir` | `~/.local/state/tuigram`; `XDG_STATE_HOME` overrides base |
| `poll_seconds` | 5 seconds; minimum 2 |

Config lives under the OS config directory + `tuigram/config.json`; `XDG_CONFIG_HOME` overrides its base. On macOS the default config path is `~/Library/Application Support/tuigram/config.json`, and cache is `~/Library/Caches/tuigram`. Linux/BSD normally use `~/.config/tuigram` and `~/.cache/tuigram`. Paths in the JSON must be absolute. Data directories require mode `0700`, files `0600`; symlinks and unsafe permissions are rejected. On macOS use canonical `/private/tmp` paths for temporary custom directories.

Only explicitly previewed images are cached. The cache enforces a total payload-byte limit, an 8 MiB per-file limit, and a 1,024-file limit. Oldest files are evicted first. Filesystem metadata is outside the byte budget. Cached images are **not encrypted**; set the cache size to `0` if you do not want persistent media. Message history, contact lists, and drafts stay in memory. Clearing the cache preserves the encrypted login session.

## Current limits

- Up to 1,000 recent conversations and 100 messages per history/search request; older-history pagination is not yet exposed.
- Up to 100 contact search results; discovery is constrained by Telegram's privacy settings and API behavior.
- Polling updates; no read receipts, typing indicators, or notifications yet.
- Text sending and forwarding are implemented. Uploads, message editing/deletion, voice/video playback, stickers, and custom emoji artwork are not implemented.
- Image previews are limited to 8 MiB downloads and 16 megapixels decoded. Unsupported formats remain attachment labels.
- Channel posting, forwarding, and reactions depend on your permissions and the chat's Telegram settings. API/flood-wait errors are displayed; failed sends are not automatically retried.
- Telegram cloud chats are supported. Secret chats are not supported.

## Development

```sh
make test       # unit + account-free functional tests
make race       # race detector and coverage
make smoke      # built CLI: help/version/demo/config/cache
make vet
make vuln       # reachable dependency vulnerability scan
```

See [development and contribution conventions](docs/DEVELOPMENT.md), [security and local data](SECURITY.md), and the [MIT license](LICENSE). CI runs unit/functional tests, race checks, smoke tests, and cross-platform binary builds. None of these tests sends messages through your Telegram account.
