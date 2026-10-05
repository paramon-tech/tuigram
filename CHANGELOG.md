# Changelog

## [0.1.0] — Unreleased

First release of Tuigram, a keyboard-driven Telegram client for the terminal.

### Added

- Telegram phone/code, two-factor, and QR login with an encrypted local session,
  plus an account-free demo mode.
- Chat and contact management, forwarding, media downloads, image previews,
  and playback of voice messages.
- Photo, video, and document uploads, media albums, captions, and emoji reactions.
- Chat organization with pinning, archiving, muting, Telegram folders, and an
  unread-only view.
- In-app settings for themes, refresh timing, download location, read receipts,
  microphone selection, and configurable action shortcuts.
- Search across available chat history, older/newer pagination, and jumps to
  the earliest or latest messages and search results.
- Native one-to-one voice calls with incoming/outgoing controls, microphone
  mute, hangup, and FFmpeg/ffplay audio support.
- Audio setup commands: `audio check`, `audio devices`, and `audio speaker-test`.
  Call controls show incoming/outgoing packet counts, decoded audio levels,
  and player errors without recording conversations.
- Release packaging for Linux, macOS, FreeBSD, and OpenBSD on amd64 and arm64.

### Fixed

- Spaces now work in message composition and text forms.
- Viewed, unfiltered messages are marked read without acknowledging newer or
  hidden messages. Failed receipts respect cooldowns and retry when eligible,
  including while viewing older history.
- Corrected voice-call channel negotiation that could confuse acknowledgment
  of the local stream with the remote stream, leaving incoming audio silent.
  Independent offers, simultaneous offers, duplicate offers, and delayed
  answers are handled separately.
- Unexpected receive and decoder failures are visible in call controls;
  hanging up releases blocked audio receivers.
- Telegram `FLOOD_WAIT` and `FLOOD_PREMIUM_WAIT` responses prevent further
  requests to the affected API method until the server's wait expires.
  Background refreshes pause, and failed read receipts back off.
- Superseded history requests are canceled, repeated pending navigation is
  deduplicated, and background polling no longer repeats explicit searches.
- Speaker diagnostics report output-device errors even if ffplay exits with
  a successful exit status.

### Changed

- The default refresh interval is 15 seconds instead of 5. Existing saved
  intervals are preserved.
- Background chat refreshes reuse folder rules for one minute; opening
  organization or editing a folder fetches fresh rules.
- The one-to-one call transport uses a scoped copy of gotd v0.162.0 with the
  negotiation fixes. Its upstream MIT license is included in release archives.
- Release archives include this changelog.

### Release qualification

- Native calls remain experimental. Synthetic decoding and bidirectional
  encrypted transport tests pass; two-way audible speech with official
  Telegram clients still needs live confirmation after the negotiation fix.
- Calls require FFmpeg with libopus and ffplay. Group/video calls, echo
  cancellation, and Telegram's custom reflector relays are not supported.
  Relay-only calls are refused to honor peer-to-peer privacy settings.
- Server cooldowns remain in effect after restarting the app. Failed final
  send operations are not automatically replayed.
- BSD artifacts require native testing before release publication.

[0.1.0]: https://github.com/paramon-tech/tuigram/tree/release/v0.1.0
