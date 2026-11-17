# Changelog

## [0.2.0] - 2026-11-18

The files page: root's files in the browser, beside the terminal. For openccu-lite 2.0.0-beta.7 and newer, as
before.

- **A file browser and an editor**, in a tab of its own (*Open files* on the addon's page): a tree of the system's
  folders beside an editor (Monaco) that colours shell scripts, JSON (with validation), INI and `*.conf`, systemd
  units, XML, YAML, Tcl, Lua, Python, JavaScript, HTML, CSS, Markdown and Dockerfiles. On a phone the editor is a
  plain text field.
- **Still no code of the addon runs as root, and its service opens no file of the system.** The files go over the
  SFTP part of an SSH sign-in to the system's own SSH server, with root's password, asked for that session. A
  files session is a session like a terminal: the same sign-in sheet, the same limits (both kinds count against
  the one limit), the same ends, the same bar - *Shell (root)*, now with the kind of session beside it.
- **Every file opens read-only**; *Edit* makes it writable. A new setting makes the files page read-only
  altogether.
- **The read-only root file system is shown as such**: outside the writable mounts folders carry a lock, and
  *Edit*, *New*, *Rename*, *Delete* and *Upload* are switched off and say why. The addon never remounts anything.
- **A save keeps** the file's mode, owner and group, its line ends, a missing last line end and a byte order
  mark; it refuses when the file changed on the system since it was opened (*Overwrite*, *Reload*, *Compare*); it
  writes a file beside the old one and renames it into place, or writes in place where that is impossible, and
  says so. A full disk leaves the file as it was.
- **Every overwritten file is backed up first** to `/usr/local/shell-backups`, as root, closed to everyone else
  and outside the system backup: the last 10 versions of each file, 50 MB in all, with *Compare* and *Restore*.
  The backups stay when the addon is uninstalled.
- **Binary files** as a hex view of their first 64 KiB; text up to 2 MB is edited, up to 10 MB shown, above that
  downloaded. Devices, pipes and sockets are never opened.
- **Upload** (64 MB a file, streamed; a replaced file is backed up first), **download**, new file and folder,
  rename (never over something that exists), delete (a folder with something in it wants its name typed), mode
  and owner of one entry.
- **The files page cannot be framed** and loads its own scripts only, the editor's workers included. The session's
  handle travels in a header, never in an address; a download goes through a ticket for that one download.
- **Audit lines** for every change: `file-write`, `file-create`, `file-delete`, `file-rename`, `file-chmod`,
  `file-chown`, `file-upload`, `file-download`, `file-restore` - the path, never what a file holds.
- The package grows from 3 MB to about 5 MB: the editor, which a browser loads when the first file is opened.

Not in it yet: a sign-in without root's password for systems that take keys only; downloading a folder; a search
through files.

## [0.1.0] - 2026-11-17

The first version, for openccu-lite 2.0.0-beta.7 and newer (the first whose session check can be asked without
keeping the web session alive).

- **A root shell in the browser**, for when no ssh client is at hand: a terminal (xterm.js) in a tab of its own,
  with resize, copy and paste, and 5000 lines of scrollback in the browser.
- **The addon is never root.** It runs confined as its own user, with no privilege, no port and no scope on the
  system's API. A session is an SSH sign-in to the system's own SSH server on the loopback, with root's password,
  which is asked for every session and stored nowhere. SSH has to be switched on (*System → Remote access*); the
  addon never switches it on, and *SSH off* switches the shell off. The SSH server's host key is checked against the
  system's own host key files.
- **Off after the install** - every install, a reinstall over kept settings too. An administrator switches it on for 1 hour, 8 hours (the default) or until it is
  switched off again; a dialog says what that means. While it is on, the system's Status page carries a notice.
- **Limits:** a session ends after 15 minutes without input (output does not count), after 8 hours at the latest,
  and at most 2 sessions are open at once (all three can be set). Five wrong passwords lock the sign-in for 15
  minutes.
- **For administrators only**, with their web session - never an API token. A session ends with the web session
  that opened it: a logout, an expiry, a lowered account (asked every 15 seconds, in a way that does not count as
  activity). It also ends when the addon is switched off, when SSH is switched off, and when its tab stays closed
  for more than 30 seconds (a reload comes back to the session).
- **Over HTTPS only.** Through a tunnel addon (WireGuard, Tailscale) the shell is refused unless an administrator
  allows it in the settings.
- **The terminal page cannot be framed** and loads its own scripts only; the session's handle lives in the tab, in
  no address and no cookie. A bar that never goes away says *Shell (root)*, who, from where, since when and when
  the session ends without input.
- **A paste of several lines asks first**, once per session.
- **Audit lines** in the system's log and on the addon's page: who switched the shell on and off, who opened and
  closed a session, from where and why. What is typed and shown is never logged.
- German and English, light and dark.

Not in it yet: a file browser and an editor, and a sign-in without root's password for systems that take keys only.
