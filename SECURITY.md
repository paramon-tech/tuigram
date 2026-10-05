# Security and local data

Tuigram is an early-stage third-party Telegram client. Its security controls are implemented and tested, but it has not had an independent audit or live-account release qualification.

## Stored data

- `session.enc` contains the Telegram authorization session, encrypted with AES-256-GCM. Each write uses a fresh 16-byte salt and GCM nonce. The key is derived using scrypt (`N=32768`, `r=8`, `p=1`); the format header and salt are authenticated. No key or passphrase is written to disk.
- Config and state directories are private (`0700`); files use `0600`. Writes use an exclusive temporary file, file sync, atomic rename, and directory sync. Unsafe symlinks, hard links, nonregular files, ownership, and permissions are rejected. A process lock prevents two clients from updating the same session concurrently.
- Only requested image previews enter the media cache. **Images are plaintext** with private file permissions, bounded size, count, and TTL. Set `cache_max_bytes` to `0` to disable preview caching. Cache clearing does not remove the session.
- Explicit attachment downloads are plaintext files in `~/Downloads/tuigram` (`0700` directory, `0600` files). They remain until you remove them and are outside the cache's TTL, size limit, and clear operation. Individual downloads are capped at 512 MiB. Failed or canceled downloads are removed; remote filenames cannot select paths or overwrite existing files.
- Voice playback creates a private plaintext temporary file, launches an installed audio player, and removes the copy when playback ends or is stopped. Protected or disappearing media is rejected for downloads and external playback.
- Photo/video uploads read the explicitly selected local file and stream it to Telegram. Tuigram does not create a persistent upload copy. Inspection and upload use the same open file, reads are size-bounded, and a detected change or truncation prevents the final send. Photo uploads are capped at 10 MiB and video uploads at 512 MiB. Captions and selected paths are kept in memory.
- Native calls do not write recordings to disk. Microphone audio is encoded by FFmpeg and reaches tuigram over an ephemeral localhost UDP socket; received Opus audio reaches ffplay through an Ogg pipe. These local audio hops are unencrypted. The call library handles Telegram signaling and the WebRTC network transport. Audio processes, sockets, and pipes are closed when a call ends or the application exits.
- Cache keys include the authenticated account, media identity, and message edit revision. Protected and expiring media bypass disk caching. Cached copies of ordinary media may remain until TTL/eviction even after the server removes a message; use `cache clear` when needed.
- Messages, contacts, access hashes, drafts, and call state remain in memory. Tuigram does not write application logs or send application analytics. Telegram API traffic is handled by the native Go MTProto library; calls also use its WebRTC transport.

The local passphrase differs from Telegram's two-step verification password. Keep it in a password manager; there is no recovery mechanism. Passphrase byte buffers are cleared when practical, but Go's runtime and third-party code may retain copies in process memory. Encryption cannot protect an unlocked process or a compromised user account. Use full-disk encryption where appropriate.

## Terminal and media handling

Remote names, text, URLs, reaction labels, and errors are sanitized before terminal rendering, removing control sequences and bidirectional control characters. Tuigram does not execute remote text or automatically open message links. Voice playback explicitly launches ffplay, mpv, or play; filenames are passed as individual arguments without a shell, and the player's terminal input/output is disconnected. Native calls launch FFmpeg and ffplay with separate arguments and bounded diagnostic output. Attachment paths are opened directly and are never evaluated by a shell. Tuigram does not copy text to the clipboard or launch external image viewers. QR login output is generated locally from Telegram's login token. Treat that QR code like a temporary login credential.

Incoming calls do not automatically open the microphone. Capture starts only after an explicit start/answer action and an established call connection. Muting drops outgoing audio but leaves capture running; ending the call stops capture and playback. `audio check` inspects installed tool capabilities without opening audio devices. `audio devices` enumerates devices and may request operating-system permission, but does not record. There is no echo cancellation; headphones help keep speaker audio out of the microphone.

Image preview downloads are capped at 8 MiB. The renderer checks image dimensions before decoding and caps decoded images at 16 megapixels. Explicit attachment downloads stream to disk with both metadata and actual bytes checked against the 512 MiB limit and a five-minute deadline. Uploads validate JPEG/PNG headers or bounded MP4/MOV metadata and have a ten-minute deadline. Failed sends are not automatically retried; after a canceled or ambiguous send, inspect the chat before retrying. API calls made by the UI have deadlines and stale responses cannot replace a newly selected conversation.

Native calling has automated lifecycle and local audio tests but has not completed live-account verification or an independent security audit. The pinned transport does not implement Telegram's custom reflector relay protocol, which limits connection compatibility on some networks. Relay-only calls are refused before starting the media transport or exchanging connection candidates, honoring Telegram's peer-to-peer privacy settings. Only one-to-one voice calls are implemented.

Telegram cloud chats are not end-to-end encrypted. Tuigram does not implement Telegram secret chats. Normal Telegram account limits, privacy settings, and authorization rules apply.

## If a session may be compromised

Use an official Telegram client to terminate the relevant session under **Settings → Devices**. Removing a local file does not revoke Telegram authorization. Then stop tuigram, remove the encrypted session from your configured state directory, and sign in again with a new local passphrase. If you lose only the local passphrase, use the same revoke-and-sign-in flow. `cache clear` removes preview files; it does not promise forensic secure erasure on SSDs or snapshots.

## Reporting

Use GitHub's private vulnerability reporting on this repository if the maintainer has enabled it. Do not include credentials, session files, QR tokens, private messages, or API hashes in public issues. For non-sensitive bugs, include the version, OS/architecture, terminal, and a reproduction using `--demo` where possible.
