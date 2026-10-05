# tuigram

Telegram, at keyboard speed. A native Go terminal client for private conversations, groups, and channels, with vim-style navigation and a small local footprint.

**Early development:** the native Telegram integration is implemented and covered by mocked RPC tests. Live account authentication, messaging, uploads, and voice calls still need manual verification. The demo and functional tests run without credentials.

```sh
go build -o bin/tuigram ./cmd/tuigram
./bin/tuigram --demo
```

Requires Go 1.26 or newer to build. No TDLib, C compiler, bot token, or database is required. Press `?` inside the app for help; `q` quits. A headless preview is available with `--demo --snapshot`.

## Features

| Feature | Available behavior |
| --- | --- |
| Conversations | Browse private chats, groups, supergroups, and channels; start private chats, create groups, rename groups/channels, delete private history or leave groups/channels |
| Messaging | Read and send text, multiline composition, per-chat in-memory drafts |
| Attachments | Send photos, videos, and documents; up to 10 files as an album; optional captions and send-as-file mode |
| Forwarding | Forward a selected message to another conversation |
| Reactions | Emoji picker, replace/remove your reaction, counts and your-reaction indicator |
| Rich content | Unicode emoji, visible URL destinations, forwarded indicators, attachment labels |
| Images | Download on demand and preview PNG/JPEG/GIF with terminal color blocks; first frame for GIF |
| Media downloads | Save selected images, videos, audio, and documents to `~/Downloads/tuigram` |
| Voice messages | Play and stop selected voice messages using an installed audio player |
| Voice calls | Native one-to-one outgoing/incoming calls, answer/decline, microphone mute, and hangup |
| Search | Search the entire available chat history with older/newer pages and a jump to the beginning; find contacts and public usernames |
| Contacts | Browse saved contacts, search public usernames, add by search or phone, edit names, and delete contacts |
| Login | Phone/code, Telegram two-step verification, or a renewing QR code |
| Navigation | Vim-style movement, pane switching, scrollable message reader, keyboard-only dialogs |
| Organization | Pin, archive, mute, Telegram folders, and unread-only filtering |
| Read state | Automatically mark viewed history read and synchronize remaining unread counts |
| Settings | In-app theme, download directory, refresh interval, read policy, microphone preferences, and action shortcuts |
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

Voice-message playback requires **ffplay** (from FFmpeg), **mpv**, or **play** (from SoX) on `PATH`, with support for the message's audio codec. Telegram voice messages commonly use Ogg Opus.

Native calls require **ffmpeg** with the **libopus** encoder and an **ffplay** build with audio output support on `PATH`. The tuigram binary remains pure Go; those tools handle microphone capture and speaker playback. On macOS, install [Homebrew's FFmpeg package](https://formulae.brew.sh/formula/ffmpeg):

```sh
brew install ffmpeg
./bin/tuigram audio check
```

`audio check` checks the installed tools, encoder, and capture backend without opening a microphone or speaker. It does not verify microphone permissions, device availability, or a Telegram connection.

Run `./bin/tuigram audio speaker-test` to play a quiet two-second tone through the system output, without opening the microphone or signing in to Telegram. If the tone is silent, check the selected output device and volume in your operating system.

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
| `gg` / `Home`, `G` / `End` | First/last chat; in messages, jump to beginning/latest history or search results |
| `Ctrl+u` / `Ctrl+d`, `PageUp` / `PageDown` | Move by a page; fetch more history at an edge |
| `[` / `]`, `B` / `L` | Load older/newer messages; jump to beginning/latest |
| `Enter` | Open conversation or selected picker item |
| `i` | Compose; `Enter` inserts a newline |
| `a` | Attach photos, videos, or documents by local file path |
| `Ctrl+n` / `Ctrl+d` in attachment form | Add/remove a file; up to 10 files |
| `Ctrl+f` in attachment form | Toggle automatic media / send all as files |
| `Ctrl+s` | Send composed text or the attachment form |
| `/` | Search current conversation; `Esc` clears search |
| `n` | Pick a person and start a private chat |
| `N` | Create a group: select members with `Space`, press `Enter`, then enter its title |
| `e` | Rename selected group/channel; rename private-chat people through contacts |
| `D` | Confirm deleting your private chat history or leaving a group/channel |
| `c` | Browse saved contacts; search contacts/public usernames |
| `a` in contact picker | Add selected person to Telegram contacts |
| `n` / `e` / `D` in contact picker | Add by international phone number, edit a name, or confirm removing a contact |
| `f` | Pick a destination and forward selected message |
| `r` | Open reaction picker; arrows select, `Enter` applies, `0` selects removal |
| `o` | Organization: `p` pin, `a` archive, `m` mute, `Enter` show folder |
| `n`, `+` / `-` in organization | Create folder with selected chat; add/remove chat in highlighted folder |
| `u` | Toggle unread-only conversations |
| `,` | Settings: arrows/Tab select, `Enter` change/edit, `Ctrl+s` save |
| `v` | Preview selected image |
| `d` | Download selected attachment; `Esc` stops the download |
| `p` | Play or stop selected voice message; `Esc` also stops playback |
| `C` / `Ctrl+g` | Open native call controls for the selected person or current call |
| `Enter` / `a` in call controls | Start/answer a call; `a` answers an incoming call |
| `m` / `x` in call controls | Mute/unmute microphone; hang up or decline |
| `Ctrl+x` | End or decline the current call from any pane |
| `y` | Read full message with scrolling |
| `t` | Cycle theme for the current session |
| `R` | Refresh |
| `?`, `Esc`, `q` / `Ctrl+c` | Help, close/cancel, quit |

The layout switches to a single pane in narrow terminals. Emoji appearance depends on your terminal/font; true color gives the best image previews. URLs are displayed as text and never opened automatically. The composer currently supports append, backspace, newline, and `Ctrl+u` to clear.

Chat and contact forms support `Tab` to switch fields, `Enter` to advance/save, `Ctrl+s` to save, and `Esc` to cancel. Group titles allow up to 128 characters; contact names allow up to 64. Deleting a private chat removes your history without deleting the other person's copy. Leaving a group/channel does not delete it for other members, and removing a contact keeps its messages. Rename and invitation actions depend on your Telegram permissions and the person's privacy settings.

For attachments, enter a local path, including `~/...` or a path containing spaces. Matching surrounding quotes are accepted. `Tab` switches fields, `Enter` advances or sends from the caption field, and `Ctrl+s` sends from either field. `Esc` stops an upload; a failed or stopped upload keeps the form for correction or an explicit retry. If cancellation happens during the final send, check the chat before retrying. Photos are limited to 10 MiB, with width plus height at most 10,000 pixels and aspect ratio at most 20:1. Videos and documents are limited to 512 MiB; an album is limited to 10 files and 512 MiB total. Captions allow 1,024 characters. `Ctrl+n` adds a path field, `Ctrl+d` removes the selected path, and `Ctrl+f` sends all entries as documents. Photos and videos can share an album; documents must form a separate album, or use send-as-files for the whole selection. The form caption belongs to the first attachment. Files are not transcoded; use send-as-files for unsupported image/video formats or to keep media as documents. All files are validated before uploading and verified again before publishing the album. Uploads have a ten-minute deadline. Files are sent in their existing format without conversion.

Search with `/`, then press `Enter`. Search runs against the full chat history available to your account, including messages older than the loaded page. Use `[` / `]` to load older/newer pages, `B` or `gg` / `Home` in the messages pane to jump to the earliest message or match, and `L` or `G` / `End` to return to the latest. Scrolling past a loaded edge fetches another page. Selection stays in place while pages load. Normal refresh preserves older loaded pages and does not jump you out of a historical window. Deleted messages and history unavailable to your account cannot be recovered.

Searches are not repeated by background polling. Use `R` while viewing the latest results to refresh them, or submit a new search. Moving to another chat or search cancels an obsolete history request; repeated navigation keys share the same pending request.

Viewed, unfiltered history is acknowledged on Telegram up to the selected message; opening the latest page marks the loaded conversation read. Newer messages that arrive afterward keep their unread count. Search results, hidden narrow-screen history, and overlays do not acknowledge unseen messages. Set **Mark viewed messages read** to false in settings to disable automatic acknowledgements.

Press `o` to organize the selected chat. Pinning, archiving, muting, and folder membership are saved to Telegram. Select Inbox, Archive, All chats, or a Telegram folder and press `Enter` to show it. `n` creates a folder containing the selected chat; highlight a custom folder and use `+` or `-` to change membership. Existing folder rules are preserved. Shared folders are view-only. Automatic folder rules apply to the loaded conversation window; explicitly included folder members are fetched even when outside that window. `u` filters to unread conversations; the chat you just read remains selected until you navigate away, avoiding automatic reads of successive chats.

Open a private conversation and press `C`, then `Enter` to call. Incoming calls show a caller banner; press `Ctrl+g` to open the controls, then `Enter` or `a` to answer, or `x` to decline. During a call, `m` toggles outgoing audio, `x` hangs up, and `Esc` returns to chatting while the call continues. `Ctrl+g` reopens the controls and `Ctrl+x` ends the call anywhere. One call can run at a time; groups, channels, bots, and Saved Messages cannot be called. Demo mode simulates call controls without opening audio devices or connecting to Telegram.

The microphone opens only after you explicitly start or answer a call and its connection is established. Allow microphone access when the operating system prompts. Muting stops transmitted audio while keeping the microphone open; hanging up or quitting closes the audio processes. Speaker output follows the system's selected output device. Use headphones: echo cancellation is not implemented. Native calls remain experimental and need live testing; networks that require Telegram's custom relay protocol may fail to connect.

Call controls show outgoing and incoming audio packet counts. An increasing incoming count means audio is reaching the playback queue; it does not confirm audible speaker output. **Decoded audio** shows whether the player is waiting, decoding silence, or decoding a signal, with its volume in dBFS. These measurements stay in memory and do not record the conversation. Decoder errors are shown even when the player keeps running. If the other person can hear you but you hear nothing, check these lines while they speak, then run `audio speaker-test` in a second terminal during the call to check local output.

## Configuration and cache

```sh
./bin/tuigram config init
./bin/tuigram cache stats
./bin/tuigram cache clear
./bin/tuigram --theme dracula
./bin/tuigram --config /absolute/private/path/config.json
./bin/tuigram audio devices
./bin/tuigram --call-input-device 0
# Linux example for an ALSA input instead of the default PulseAudio input:
./bin/tuigram --call-input-format alsa --call-input-device default
```

Flags precede commands. `config init` prints the path and creates a private JSON config without copying credentials from the environment. Press `,` in the app to change preferences and `Ctrl+s` to save them, or edit the JSON file. The settings panel preserves credentials and unrelated configuration; environment-only credentials are never copied to disk. Theme, download directory, read policy, and shortcut changes apply immediately. Refresh timing changes immediately; microphone changes require restarting. Demo settings apply only for the current session. Partial JSON objects inherit defaults; unknown fields are rejected.

The default refresh interval is 15 seconds; an interval already saved in your config is preserved. Folder rules are cached for one minute during background chat refreshes; opening organization or editing a folder fetches fresh rules. Telegram `FLOOD_WAIT` responses display the remaining wait and stop further requests to the affected API method until that wait expires. Background refreshes also pause, and failed read receipts back off. User actions such as sending a message are not automatically repeated; retry after the displayed wait. These cooldowns last for the current app session, so restarting does not remove a limit imposed by Telegram.

```json
{
  "theme": "midnight",
  "cache_max_bytes": 33554432,
  "cache_ttl_hours": 24,
  "poll_seconds": 15,
  "download_dir": "",
  "mark_read": true,
  "key_bindings": {},
  "call_input_format": "",
  "call_input_device": ""
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
| `poll_seconds` | 15 seconds; minimum 2 |
| `download_dir` | Empty uses `~/Downloads/tuigram`; otherwise an absolute private directory |
| `mark_read` | `true`; acknowledge viewed messages on Telegram and refresh unread counts |
| `key_bindings` | Optional action-to-key map: `compose`, `attach`, `react`, `organization`, `settings`, `search`, `theme`, `refresh`. For example `{"settings":";"}`; fixed navigation/call/quit shortcuts stay reserved |
| `call_input_format` | Empty selects `avfoundation` on macOS, `pulse` on Linux, `oss` on FreeBSD, or `sndio` on OpenBSD; `alsa` is also available when FFmpeg supports it |
| `call_input_device` | Empty selects the backend's default microphone (`/dev/dsp` for OSS); otherwise a device name or index |

`--call-input-format` and `--call-input-device` override these JSON settings for one launch. `audio devices` lists capture devices and may trigger an operating-system permission prompt; it does not record audio. For macOS, supply only the audio device name or index, such as `0`, without a video-device prefix. Device names depend on the selected [FFmpeg input backend](https://ffmpeg.org/ffmpeg-devices.html). Choose speakers or headphones in the operating system's output settings.

Config lives under the OS config directory + `tuigram/config.json`; `XDG_CONFIG_HOME` overrides its base. On macOS the default config path is `~/Library/Application Support/tuigram/config.json`, and cache is `~/Library/Caches/tuigram`. Linux/BSD normally use `~/.config/tuigram` and `~/.cache/tuigram`. Storage paths in the JSON must be absolute. Data directories require mode `0700`, files `0600`; symlinks and unsafe permissions are rejected. On macOS use canonical `/private/tmp` paths for temporary custom directories.

Only explicitly previewed images are cached. The cache enforces a total payload-byte limit, an 8 MiB per-file limit, and a 1,024-file limit. Oldest files are evicted first. Filesystem metadata is outside the byte budget. Cached images are **not encrypted**; set the cache size to `0` to disable preview caching. Message history, contact lists, and drafts stay in memory. Clearing the cache preserves the encrypted login session.

Explicit downloads are separate from the preview cache. Files saved with `d` remain in `~/Downloads/tuigram` until you remove them; `cache clear` and cache TTL/size settings do not remove them. The directory uses mode `0700` and files `0600`, but downloaded media is **not encrypted**. Each attachment is limited to 512 MiB with a five-minute download deadline. Incomplete or canceled downloads are removed. Voice playback uses a private temporary copy removed when playback finishes or stops. Protected and disappearing media cannot be saved or played externally.

## Current limits

- Up to 1,000 recent unpinned conversations per Inbox/Archive list, plus pinned chats and explicit folder members. History/search is fetched in pages of up to 100 messages, without a fixed total-history cutoff.
- Up to 100 contact search results; discovery is constrained by Telegram's privacy settings and API behavior.
- Chat/message refresh uses polling; native call signaling and incoming calls use Telegram updates. Viewed history sends read acknowledgements; typing indicators and desktop notifications are not implemented.
- Photo/video uploads, text sending, and forwarding are implemented. Individual message editing/deletion, video playback, stickers, and custom emoji artwork are not implemented.
- Voice messages require an external audio player; native calls require FFmpeg/libopus and ffplay. Group calls, video calls, and echo cancellation are not implemented. The pinned call library cannot use Telegram's custom reflector relays, so connection compatibility depends on the network and peer. Relay-only calls are unsupported and refused to honor Telegram's peer-to-peer privacy settings. Live calls remain unverified.
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

See the [changelog](CHANGELOG.md), [development and contribution conventions](docs/DEVELOPMENT.md), [security and local data](SECURITY.md), and the [MIT license](LICENSE). CI runs unit/functional tests, race checks, smoke tests, and cross-platform binary builds. None of these tests sends messages through your Telegram account.

The one-to-one call transport includes a scoped copy of gotd's call package with Telegram channel-negotiation fixes. Its [upstream provenance](internal/tgcalls/UPSTREAM) and [MIT license](internal/tgcalls/LICENSE) are retained; release archives include that license.
