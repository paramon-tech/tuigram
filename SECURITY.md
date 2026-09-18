# Security and local data

Tuigram is an early-stage third-party Telegram client. Its security controls are implemented and tested, but it has not had an independent audit or live-account release qualification.

## Stored data

- `session.enc` contains the Telegram authorization session, encrypted with AES-256-GCM. Each write uses a fresh 16-byte salt and GCM nonce. The key is derived using scrypt (`N=32768`, `r=8`, `p=1`); the format header and salt are authenticated. No key or passphrase is written to disk.
- Config and state directories are private (`0700`); files use `0600`. Writes use an exclusive temporary file, file sync, atomic rename, and directory sync. Unsafe symlinks, hard links, nonregular files, ownership, and permissions are rejected. A process lock prevents two clients from updating the same session concurrently.
- Only requested image previews enter the media cache. **Images are plaintext** with private file permissions, bounded size, count, and TTL. Set `cache_max_bytes` to `0` to disable persistent media. Cache clearing does not remove the session.
- Cache keys include the authenticated account, media identity, and message edit revision. Protected and expiring media bypass disk caching. Cached copies of ordinary media may remain until TTL/eviction even after the server removes a message; use `cache clear` when needed.
- Messages, contacts, access hashes, and drafts remain in memory. Tuigram does not write application logs or send application analytics. Network traffic is handled by the native Go MTProto library.

The local passphrase differs from Telegram's two-step verification password. Keep it in a password manager; there is no recovery mechanism. Passphrase byte buffers are cleared when practical, but Go's runtime and third-party code may retain copies in process memory. Encryption cannot protect an unlocked process or a compromised user account. Use full-disk encryption where appropriate.

## Terminal and media handling

Remote names, text, URLs, reaction labels, and errors are sanitized before terminal rendering, removing control sequences and bidirectional control characters. Tuigram does not execute remote text, open links, copy text to the clipboard, or launch external image viewers. QR login output is generated locally from Telegram's login token. Treat that QR code like a temporary login credential.

Image downloads are capped at 8 MiB. The renderer checks image dimensions before decoding and caps decoded images at 16 megapixels. API calls made by the UI have deadlines and stale responses cannot replace a newly selected conversation.

Telegram cloud chats are not end-to-end encrypted. Tuigram does not implement Telegram secret chats. Normal Telegram account limits, privacy settings, and authorization rules apply.

## If a session may be compromised

Use an official Telegram client to terminate the relevant session under **Settings → Devices**. Removing a local file does not revoke Telegram authorization. Then stop tuigram, remove the encrypted session from your configured state directory, and sign in again with a new local passphrase. If you lose only the local passphrase, use the same revoke-and-sign-in flow. `cache clear` removes preview files; it does not promise forensic secure erasure on SSDs or snapshots.

## Reporting

Use GitHub's private vulnerability reporting on this repository if the maintainer has enabled it. Do not include credentials, session files, QR tokens, private messages, or API hashes in public issues. For non-sensitive bugs, include the version, OS/architecture, terminal, and a reproduction using `--demo` where possible.
