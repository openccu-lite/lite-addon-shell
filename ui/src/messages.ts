import type {Catalogue} from 'occulite-ui/i18n';

// The pages' strings, German and English, keyed by the English text. The kit's own component
// strings come from occulite-ui. User-facing text says "the system", never "the box"; the addon
// is called "Shell" in both languages, and a session's bar says "Shell (root)".
export const MESSAGES: Catalogue = {
    'Shell': {de: 'Shell', en: 'Shell'},
    'Shell (root)': {de: 'Shell (root)', en: 'Shell (root)'},
    'Version {v}': {de: 'Version {v}', en: 'Version {v}'},
    'Loading…': {de: 'Lädt…', en: 'Loading…'},
    'The page could not read the addon\'s state: {error}': {de: 'Die Seite konnte den Zustand des Addons nicht lesen: {error}', en: 'The page could not read the addon\'s state: {error}'},
    'Cancel': {de: 'Abbrechen', en: 'Cancel'},
    'Save': {de: 'Speichern', en: 'Save'},
    'Saved.': {de: 'Gespeichert.', en: 'Saved.'},

    // the intro
    'A root shell for this system in the browser, for when no ssh client is at hand. The addon itself is not root: each session signs in to the system\'s own SSH server with root\'s password, which is asked every time and stored nowhere. If you have an ssh client, use it.': {
        de: 'Eine Root-Shell für dieses System im Browser, für den Fall, dass kein ssh-Client zur Hand ist. Das Addon selbst ist nicht root: Jede Sitzung meldet sich am SSH-Server des Systems mit dem root-Passwort an, das jedes Mal abgefragt und nirgends gespeichert wird. Wenn Sie einen ssh-Client haben, nehmen Sie den.',
        en: 'A root shell for this system in the browser, for when no ssh client is at hand. The addon itself is not root: each session signs in to the system\'s own SSH server with root\'s password, which is asked every time and stored nowhere. If you have an ssh client, use it.',
    },

    // the status card
    'Status': {de: 'Status', en: 'Status'},
    'Addon': {de: 'Addon', en: 'Addon'},
    'SSH': {de: 'SSH', en: 'SSH'},
    'Connection': {de: 'Verbindung', en: 'Connection'},
    'Open sessions': {de: 'Offene Sitzungen', en: 'Open sessions'},
    '{n} of {max}': {de: '{n} von {max}', en: '{n} of {max}'},
    'Off': {de: 'Aus', en: 'Off'},
    'On': {de: 'Ein', en: 'On'},
    'On until {time}': {de: 'Ein bis {time}', en: 'On until {time}'},
    'Keys only': {de: 'Nur Schlüssel', en: 'Keys only'},
    'Unknown': {de: 'Unbekannt', en: 'Unknown'},
    'HTTPS': {de: 'HTTPS', en: 'HTTPS'},
    'HTTP': {de: 'HTTP', en: 'HTTP'},
    'Tunnel': {de: 'Tunnel', en: 'Tunnel'},
    'Switched on by {user}.': {de: 'Eingeschaltet von {user}.', en: 'Switched on by {user}.'},
    'System → Remote access': {de: 'System → Fernzugriff', en: 'System → Remote access'},

    // why the shell is blocked
    'The system runs without a login: every visitor would be an administrator. The shell stays off.': {
        de: 'Das System läuft ohne Anmeldung: Jeder Besucher wäre Administrator. Die Shell bleibt aus.',
        en: 'The system runs without a login: every visitor would be an administrator. The shell stays off.',
    },
    'The addon is switched off. Switch it on first.': {de: 'Das Addon ist ausgeschaltet. Schalten Sie es zuerst ein.', en: 'The addon is switched off. Switch it on first.'},
    'The shell works over HTTPS only. Open the system with https:// in the address.': {
        de: 'Die Shell funktioniert nur über HTTPS. Öffnen Sie das System mit https:// in der Adresse.',
        en: 'The shell works over HTTPS only. Open the system with https:// in the address.',
    },
    'The shell is not offered through a tunnel addon. An administrator can allow it in the settings below.': {
        de: 'Über ein Tunnel-Addon wird die Shell nicht angeboten. Ein Administrator kann das unten in den Einstellungen erlauben.',
        en: 'The shell is not offered through a tunnel addon. An administrator can allow it in the settings below.',
    },
    'SSH is switched off. Switch it on under System → Remote access.': {
        de: 'SSH ist ausgeschaltet. Schalten Sie es unter System → Fernzugriff ein.',
        en: 'SSH is switched off. Switch it on under System → Remote access.',
    },
    'SSH accepts keys only, so root\'s password does not work. Allow password login under System → Remote access.': {
        de: 'SSH nimmt nur Schlüssel an, das root-Passwort funktioniert deshalb nicht. Erlauben Sie die Anmeldung mit Passwort unter System → Fernzugriff.',
        en: 'SSH accepts keys only, so root\'s password does not work. Allow password login under System → Remote access.',
    },
    'The SSH server did not answer as the system\'s own. The shell stays closed.': {
        de: 'Der SSH-Server hat sich nicht als der des Systems ausgewiesen. Die Shell bleibt geschlossen.',
        en: 'The SSH server did not answer as the system\'s own. The shell stays closed.',
    },
    'Too many sessions are open. End one first.': {de: 'Es sind zu viele Sitzungen offen. Beenden Sie zuerst eine.', en: 'Too many sessions are open. End one first.'},

    // the switch
    'Switch on': {de: 'Einschalten', en: 'Switch on'},
    'Switch off': {de: 'Ausschalten', en: 'Switch off'},
    'Open shell': {de: 'Shell öffnen', en: 'Open shell'},
    'Opens in a tab of its own.': {de: 'Öffnet in einem eigenen Tab.', en: 'Opens in a tab of its own.'},
    'Switch on the root shell?': {de: 'Die Root-Shell einschalten?', en: 'Switch on the root shell?'},
    'This switches on a root shell in the browser. Whoever controls this browser, or learns root\'s password, controls the system and every device paired with it. Use it when ssh is not available, and switch it off afterwards. Changes outside /usr/local are lost with the next system update.': {
        de: 'Damit schalten Sie eine Root-Shell im Browser ein. Wer diesen Browser kontrolliert oder das root-Passwort erfährt, kontrolliert das System und alle angelernten Geräte. Verwenden Sie sie, wenn ssh nicht zur Verfügung steht, und schalten Sie sie danach wieder aus. Änderungen außerhalb von /usr/local gehen mit dem nächsten Systemupdate verloren.',
        en: 'This switches on a root shell in the browser. Whoever controls this browser, or learns root\'s password, controls the system and every device paired with it. Use it when ssh is not available, and switch it off afterwards. Changes outside /usr/local are lost with the next system update.',
    },
    'While it is on, the system\'s Status page says so.': {de: 'Solange sie eingeschaltet ist, steht das auf der Statusseite des Systems.', en: 'While it is on, the system\'s Status page says so.'},
    'Switched on for': {de: 'Eingeschaltet für', en: 'Switched on for'},
    '1 hour': {de: '1 Stunde', en: '1 hour'},
    '{n} hours': {de: '{n} Stunden', en: '{n} hours'},
    'until I switch it off': {de: 'bis ich sie ausschalte', en: 'until I switch it off'},
    'The root shell is switched on.': {de: 'Die Root-Shell ist eingeschaltet.', en: 'The root shell is switched on.'},
    'The root shell is switched off. Open sessions were ended.': {de: 'Die Root-Shell ist ausgeschaltet. Offene Sitzungen wurden beendet.', en: 'The root shell is switched off. Open sessions were ended.'},

    // the sessions table
    'Sessions': {de: 'Sitzungen', en: 'Sessions'},
    'No session is open.': {de: 'Keine Sitzung ist offen.', en: 'No session is open.'},
    'Account': {de: 'Konto', en: 'Account'},
    'From': {de: 'Von', en: 'From'},
    'Since': {de: 'Seit', en: 'Since'},
    'Ends without input in': {de: 'Endet ohne Eingabe in', en: 'Ends without input in'},
    'End': {de: 'Beenden', en: 'End'},
    'End all': {de: 'Alle beenden', en: 'End all'},
    'yours': {de: 'Ihre', en: 'yours'},
    'The session was ended.': {de: 'Die Sitzung wurde beendet.', en: 'The session was ended.'},
    '{n} sessions were ended.': {de: '{n} Sitzungen wurden beendet.', en: '{n} sessions were ended.'},

    // the settings
    'Settings': {de: 'Einstellungen', en: 'Settings'},
    'End a session without input after (minutes)': {de: 'Sitzung ohne Eingabe beenden nach (Minuten)', en: 'End a session without input after (minutes)'},
    'Longest session (hours)': {de: 'Längste Sitzung (Stunden)', en: 'Longest session (hours)'},
    'Sessions at once': {de: 'Sitzungen gleichzeitig', en: 'Sessions at once'},
    'Allow through a tunnel addon': {de: 'Über ein Tunnel-Addon erlauben', en: 'Allow through a tunnel addon'},
    'Off by default: the firewall opens SSH to the local networks only, and a tunnel addon (WireGuard, Tailscale) would carry the shell to every tunnel peer.': {
        de: 'Standardmäßig aus: Die Firewall öffnet SSH nur für die lokalen Netze, und ein Tunnel-Addon (WireGuard, Tailscale) würde die Shell zu jedem Tunnel-Teilnehmer tragen.',
        en: 'Off by default: the firewall opens SSH to the local networks only, and a tunnel addon (WireGuard, Tailscale) would carry the shell to every tunnel peer.',
    },
    '{min} to {max}': {de: '{min} bis {max}', en: '{min} to {max}'},
    'Output does not count as input: a running command does not keep a session open.': {
        de: 'Ausgaben zählen nicht als Eingabe: Ein laufender Befehl hält eine Sitzung nicht offen.',
        en: 'Output does not count as input: a running command does not keep a session open.',
    },

    // the log
    'Log': {de: 'Protokoll', en: 'Log'},
    'Who switched the shell on and off, and who opened and closed a session, since the addon\'s start. The same lines are in the system\'s log. What is typed in a session is never logged.': {
        de: 'Wer die Shell ein- und ausgeschaltet und wer eine Sitzung geöffnet und geschlossen hat, seit dem Start des Addons. Dieselben Zeilen stehen im Protokoll des Systems. Was in einer Sitzung getippt wird, wird nie protokolliert.',
        en: 'Who switched the shell on and off, and who opened and closed a session, since the addon\'s start. The same lines are in the system\'s log. What is typed in a session is never logged.',
    },
    'Nothing yet.': {de: 'Noch nichts.', en: 'Nothing yet.'},

    // the terminal tab: the sign-in sheet
    'root\'s password': {de: 'root-Passwort', en: 'root\'s password'},
    'Signed in to the system as': {de: 'Am System angemeldet als', en: 'Signed in to the system as'},
    'Connect': {de: 'Verbinden', en: 'Connect'},
    'Connecting…': {de: 'Verbinde…', en: 'Connecting…'},
    'You are opening a root shell on the system. Every session is written to the system\'s log; what you type is not.': {
        de: 'Sie öffnen eine Root-Shell auf dem System. Jede Sitzung wird im Protokoll des Systems vermerkt; was Sie tippen, nicht.',
        en: 'You are opening a root shell on the system. Every session is written to the system\'s log; what you type is not.',
    },
    'The password is asked for every session and is stored nowhere.': {de: 'Das Passwort wird für jede Sitzung abgefragt und nirgends gespeichert.', en: 'The password is asked for every session and is stored nowhere.'},
    'Wrong password. {n} tries left.': {de: 'Falsches Passwort. Noch {n} Versuche.', en: 'Wrong password. {n} tries left.'},
    'Wrong password. Too many failed attempts: try again at {time}.': {de: 'Falsches Passwort. Zu viele Fehlversuche: Versuchen Sie es um {time} wieder.', en: 'Wrong password. Too many failed attempts: try again at {time}.'},
    'Too many failed attempts. Try again at {time}.': {de: 'Zu viele Fehlversuche. Versuchen Sie es um {time} wieder.', en: 'Too many failed attempts. Try again at {time}.'},
    'You are not signed in to the system as an administrator (any more). Sign in again.': {
        de: 'Sie sind am System nicht (mehr) als Administrator angemeldet. Melden Sie sich erneut an.',
        en: 'You are not signed in to the system as an administrator (any more). Sign in again.',
    },
    'The sign-in failed. The addon\'s log says why.': {de: 'Die Anmeldung ist fehlgeschlagen. Das Protokoll des Addons nennt den Grund.', en: 'The sign-in failed. The addon\'s log says why.'},
    'To the addon\'s page': {de: 'Zur Seite des Addons', en: 'To the addon\'s page'},

    // the terminal tab: the bar
    'from {client}': {de: 'von {client}', en: 'from {client}'},
    'since {time}': {de: 'seit {time}', en: 'since {time}'},
    'ends without input in': {de: 'endet ohne Eingabe in', en: 'ends without input in'},
    'End session': {de: 'Sitzung beenden', en: 'End session'},
    'New session': {de: 'Neue Sitzung', en: 'New session'},
    'Session ended': {de: 'Sitzung beendet', en: 'Session ended'},
    'Reconnecting…': {de: 'Verbinde neu…', en: 'Reconnecting…'},

    // why a session ended
    'end.exit': {de: 'Die Shell wurde verlassen.', en: 'The shell was left.'},
    'end.idle': {de: 'Zu lange ohne Eingabe.', en: 'No input for too long.'},
    'end.max': {de: 'Die längste Sitzungsdauer ist erreicht.', en: 'The longest session\'s length was reached.'},
    'end.signed-out': {de: 'Ihre Anmeldung am System ist beendet.', en: 'Your sign-in to the system ended.'},
    'end.ended': {de: 'Von Ihnen oder einem anderen Administrator beendet.', en: 'Ended by you or another administrator.'},
    'end.addon-off': {de: 'Das Addon wurde ausgeschaltet.', en: 'The addon was switched off.'},
    'end.ssh-off': {de: 'SSH wurde ausgeschaltet.', en: 'SSH was switched off.'},
    'end.tab-closed': {de: 'Der Tab war zu lange geschlossen.', en: 'The tab was closed for too long.'},
    'end.connection': {de: 'Die SSH-Verbindung ist abgebrochen.', en: 'The SSH connection was lost.'},
    'end.stopping': {de: 'Der Dienst des Addons wurde beendet (Update oder Neustart).', en: 'The addon\'s service stopped (an update or a restart).'},
    'end.unverified': {de: 'Das System konnte nicht nach Ihrer Anmeldung gefragt werden.', en: 'The system could not be asked about your sign-in.'},
    'end.replaced': {de: 'Die Sitzung wurde in einem anderen Tab geöffnet.', en: 'The session was opened in another tab.'},
    'end.lost': {de: 'Die Verbindung zum System ist abgebrochen.', en: 'The connection to the system was lost.'},

    // paste
    'Paste {n} lines?': {de: '{n} Zeilen einfügen?', en: 'Paste {n} lines?'},
    'Every line runs as root as soon as it arrives. This is asked once per session.': {
        de: 'Jede Zeile wird als root ausgeführt, sobald sie ankommt. Diese Frage kommt einmal pro Sitzung.',
        en: 'Every line runs as root as soon as it arrives. This is asked once per session.',
    },
    'Paste': {de: 'Einfügen', en: 'Paste'},
};
