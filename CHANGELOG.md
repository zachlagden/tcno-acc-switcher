# Changelog

All notable changes to this fork of the TcNo Account Switcher. Upstream history lives at
https://github.com/TCNOco/TcNo-Acc-Switcher.

## [Unreleased]

## [4.1.0] - 2026-09-10

First release from this fork, built on upstream 4.0.7.

### Added
- Wispr Flow platform: save, add and switch Wispr Flow accounts on Windows and macOS. Wispr is
  closed with its own `--quit-app` command instead of being force-killed.
- Wispr Flow stats page: words, words per minute, speaking time, streaks and last dictation for
  every saved account, with an all-accounts total. Reads each account's saved snapshot, or fetches
  live numbers from Wispr and renews expired saved sessions.
- Riot session keepalive: saved Riot accounts have their refresh tokens renewed in the background
  so they do not expire between switches.
- `JSON_SELECT::<file>::<path>` as a descriptor value source for variables, suggested names and
  profile images.

### Changed
- Updates now come only from releases of zachlagden/tcno-acc-switcher, verified against this
  fork's own signing key. The platform list updates from the same releases, and the tcno.co
  version check is no longer used.
- The platform catalog is versioned 4.1.0, so it replaces an upstream catalog already on disk.
  Fork-only entries are also restored or refreshed whenever the on-disk catalog lacks or differs
  from them.

### Fixed
- Closing a platform from the tray now uses the platform's quit command, so Steam and Wispr Flow
  exit cleanly instead of waiting out a timeout and being force-killed.
