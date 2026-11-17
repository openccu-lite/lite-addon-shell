# Changelog

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
- **Off after the install.** An administrator switches it on for 1 hour, 8 hours (the default) or until it is
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
