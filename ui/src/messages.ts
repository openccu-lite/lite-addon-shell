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
    'A root shell and root\'s files for this system in the browser, for when no ssh client is at hand. The addon itself is not root: each session signs in to the system\'s own SSH server with root\'s password, which is asked every time and stored nowhere. If you have an ssh client, use it.': {
        de: 'Eine Root-Shell und die Dateien von root für dieses System im Browser, für den Fall, dass kein ssh-Client zur Hand ist. Das Addon selbst ist nicht root: Jede Sitzung meldet sich am SSH-Server des Systems mit dem root-Passwort an, das jedes Mal abgefragt und nirgends gespeichert wird. Wenn Sie einen ssh-Client haben, nehmen Sie den.',
        en: 'A root shell and root\'s files for this system in the browser, for when no ssh client is at hand. The addon itself is not root: each session signs in to the system\'s own SSH server with root\'s password, which is asked every time and stored nowhere. If you have an ssh client, use it.',
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
    'Who switched the shell on and off, who opened and closed a session, and which file was changed, since the addon\'s start. The same lines are in the system\'s log. What is typed in a session and what a file holds is never logged.': {
        de: 'Wer die Shell ein- und ausgeschaltet, wer eine Sitzung geöffnet und geschlossen hat und welche Datei geändert wurde, seit dem Start des Addons. Dieselben Zeilen stehen im Protokoll des Systems. Was in einer Sitzung getippt wird und was in einer Datei steht, wird nie protokolliert.',
        en: 'Who switched the shell on and off, who opened and closed a session, and which file was changed, since the addon\'s start. The same lines are in the system\'s log. What is typed in a session and what a file holds is never logged.',
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
    'end.stuck': {de: 'Eine Datei hat auf das Lesen nicht geantwortet. Die Sitzung wurde beendet.', en: 'A file did not answer when it was read. The session was ended.'},
    'end.replaced': {de: 'Die Sitzung wurde in einem anderen Tab geöffnet.', en: 'The session was opened in another tab.'},
    'end.lost': {de: 'Die Verbindung zum System ist abgebrochen.', en: 'The connection to the system was lost.'},

    // paste
    'Paste {n} lines?': {de: '{n} Zeilen einfügen?', en: 'Paste {n} lines?'},
    'Every line runs as root as soon as it arrives. This is asked once per session.': {
        de: 'Jede Zeile wird als root ausgeführt, sobald sie ankommt. Diese Frage kommt einmal pro Sitzung.',
        en: 'Every line runs as root as soon as it arrives. This is asked once per session.',
    },
    'Paste': {de: 'Einfügen', en: 'Paste'},

    // the addon's page: the files page
    'Open files': {de: 'Dateien öffnen', en: 'Open files'},
    'Kind': {de: 'Art', en: 'Kind'},
    'Terminal': {de: 'Terminal', en: 'Terminal'},
    'Files': {de: 'Dateien', en: 'Files'},
    'The files page only shows, and changes nothing': {de: 'Die Dateiseite zeigt nur an und ändert nichts', en: 'The files page only shows, and changes nothing'},

    // the files tab: the sign-in sheet
    'You are opening root\'s access to the system\'s files. Every session and every change is written to the system\'s log; what a file holds is not.': {
        de: 'Sie öffnen den Zugriff von root auf die Dateien des Systems. Jede Sitzung und jede Änderung wird im Protokoll des Systems vermerkt; was in einer Datei steht, nicht.',
        en: 'You are opening root\'s access to the system\'s files. Every session and every change is written to the system\'s log; what a file holds is not.',
    },
    'The system\'s SSH server offers no file access (SFTP).': {de: 'Der SSH-Server des Systems bietet keinen Dateizugriff (SFTP) an.', en: 'The system\'s SSH server offers no file access (SFTP).'},
    'The session is over.': {de: 'Die Sitzung ist beendet.', en: 'The session is over.'},

    // the files tab: the tree
    'Folders': {de: 'Ordner', en: 'Folders'},
    'Places': {de: 'Orte', en: 'Places'},
    'Folder': {de: 'Ordner', en: 'Folder'},
    'File': {de: 'Datei', en: 'File'},
    'Config': {de: 'Konfig', en: 'Config'},
    'User files': {de: 'Benutzerdaten', en: 'User files'},
    'Addons': {de: 'Addons', en: 'Addons'},
    'System': {de: 'System', en: 'System'},
    'USB': {de: 'USB', en: 'USB'},
    'Go': {de: 'Öffnen', en: 'Go'},
    'In': {de: 'In', en: 'In'},
    'In {folder}': {de: 'In {folder}', en: 'In {folder}'},
    'Empty.': {de: 'Leer.', en: 'Empty.'},
    '{path} is a link to {real}.': {de: '{path} ist ein Link auf {real}.', en: '{path} is a link to {real}.'},
    'This folder holds more than is shown here.': {de: 'Dieser Ordner enthält mehr, als hier gezeigt wird.', en: 'This folder holds more than is shown here.'},
    'Nothing selected.': {de: 'Nichts ausgewählt.', en: 'Nothing selected.'},
    'New file': {de: 'Neue Datei', en: 'New file'},
    'New folder': {de: 'Neuer Ordner', en: 'New folder'},
    'Upload': {de: 'Hochladen', en: 'Upload'},
    'Refresh': {de: 'Aktualisieren', en: 'Refresh'},
    'Download': {de: 'Herunterladen', en: 'Download'},
    'Rename': {de: 'Umbenennen', en: 'Rename'},
    'Delete': {de: 'Löschen', en: 'Delete'},
    'Properties': {de: 'Eigenschaften', en: 'Properties'},
    'Only a file is downloaded.': {de: 'Heruntergeladen wird nur eine Datei.', en: 'Only a file is downloaded.'},

    // why something cannot be changed
    'This part of the system is read-only. A change here would not survive the next system update. Settings live under /usr/local/etc/config.': {
        de: 'Dieser Teil des Systems ist schreibgeschützt. Eine Änderung hier würde das nächste Systemupdate nicht überstehen. Einstellungen liegen unter /usr/local/etc/config.',
        en: 'This part of the system is read-only. A change here would not survive the next system update. Settings live under /usr/local/etc/config.',
    },
    'These files belong to the kernel and are not changed here.': {de: 'Diese Dateien gehören dem Kernel und werden hier nicht geändert.', en: 'These files belong to the kernel and are not changed here.'},
    'The backups are written by saves only. They can be compared, restored and deleted.': {
        de: 'Die Sicherungen entstehen nur beim Speichern. Sie lassen sich vergleichen, wiederherstellen und löschen.',
        en: 'The backups are written by saves only. They can be compared, restored and deleted.',
    },
    'The files page is set to read-only in the addon\'s settings.': {de: 'Die Dateiseite ist in den Einstellungen des Addons auf Nur-Lesen gestellt.', en: 'The files page is set to read-only in the addon\'s settings.'},
    'This is not a text file. Its first bytes are shown; download it to work on it.': {
        de: 'Das ist keine Textdatei. Gezeigt werden ihre ersten Bytes; laden Sie sie herunter, um damit zu arbeiten.',
        en: 'This is not a text file. Its first bytes are shown; download it to work on it.',
    },
    'Files over 2 MB are shown, not edited. Download the file to work on it.': {
        de: 'Dateien über 2 MB werden angezeigt, nicht bearbeitet. Laden Sie die Datei herunter, um damit zu arbeiten.',
        en: 'Files over 2 MB are shown, not edited. Download the file to work on it.',
    },
    'A link has no mode or owner of its own.': {de: 'Ein Link hat keine eigenen Rechte und keinen eigenen Besitzer.', en: 'A link has no mode or owner of its own.'},

    // what the system refused
    'No such file or folder (any more).': {de: 'Diese Datei oder diesen Ordner gibt es nicht (mehr).', en: 'No such file or folder (any more).'},
    'Permission denied.': {de: 'Zugriff verweigert.', en: 'Permission denied.'},
    'No space left on the file system.': {de: 'Kein Platz mehr auf dem Dateisystem.', en: 'No space left on the file system.'},
    'That name is taken.': {de: 'Diesen Namen gibt es schon.', en: 'That name is taken.'},
    'The file is too large.': {de: 'Die Datei ist zu groß.', en: 'The file is too large.'},
    'Devices, pipes and sockets are not opened.': {de: 'Geräte, Pipes und Sockets werden nicht geöffnet.', en: 'Devices, pipes and sockets are not opened.'},
    'That is not what it was a moment ago. Refresh the folder.': {de: 'Das ist nicht mehr, was es eben war. Aktualisieren Sie den Ordner.', en: 'That is not what it was a moment ago. Refresh the folder.'},
    'The system did not take that name or value.': {de: 'Das System hat diesen Namen oder Wert nicht angenommen.', en: 'The system did not take that name or value.'},
    'The backup could not be written, so the file was not replaced.': {
        de: 'Die Sicherung konnte nicht geschrieben werden, deshalb wurde die Datei nicht ersetzt.',
        en: 'The backup could not be written, so the file was not replaced.',
    },
    'The system refused: {message}': {de: 'Das System hat abgelehnt: {message}', en: 'The system refused: {message}'},

    // the files tab: the editor
    'Choose a file on the left. It opens read-only.': {de: 'Wählen Sie links eine Datei. Sie wird schreibgeschützt geöffnet.', en: 'Choose a file on the left. It opens read-only.'},
    'Read-only': {de: 'Nur lesen', en: 'Read-only'},
    'Editing': {de: 'Bearbeiten', en: 'Editing'},
    'Binary': {de: 'Binär', en: 'Binary'},
    'Too large': {de: 'Zu groß', en: 'Too large'},
    'Comparing': {de: 'Vergleich', en: 'Comparing'},
    'Edit': {de: 'Bearbeiten', en: 'Edit'},
    'Reload': {de: 'Neu laden', en: 'Reload'},
    'Back': {de: 'Zurück', en: 'Back'},
    'Nothing to edit.': {de: 'Nichts zu bearbeiten.', en: 'Nothing to edit.'},
    'File content': {de: 'Inhalt der Datei', en: 'File content'},
    'The editor could not be loaded: {error}': {de: 'Der Editor konnte nicht geladen werden: {error}', en: 'The editor could not be loaded: {error}'},
    'Saved {time}': {de: 'Gespeichert {time}', en: 'Saved {time}'},
    'Saved {time}, written in place': {de: 'Gespeichert {time}, an Ort und Stelle geschrieben', en: 'Saved {time}, written in place'},
    'Not saved: {reason}': {de: 'Nicht gespeichert: {reason}', en: 'Not saved: {reason}'},
    'The file changed on the system.': {de: 'Die Datei hat sich auf dem System geändert.', en: 'The file changed on the system.'},
    'The file changed on the system': {de: 'Die Datei hat sich auf dem System geändert', en: 'The file changed on the system'},
    '{name} is not what it was when you opened it: someone or something wrote it at {time}. Your text is still in the editor.': {
        de: '{name} ist nicht mehr, was es beim Öffnen war: Jemand oder etwas hat die Datei um {time} geschrieben. Ihr Text steht weiter im Editor.',
        en: '{name} is not what it was when you opened it: someone or something wrote it at {time}. Your text is still in the editor.',
    },
    'Overwrite': {de: 'Überschreiben', en: 'Overwrite'},
    'Compare': {de: 'Vergleichen', en: 'Compare'},
    'The comparison needs the large editor, which a small screen does not load.': {
        de: 'Der Vergleich braucht den großen Editor, den ein kleiner Bildschirm nicht lädt.',
        en: 'The comparison needs the large editor, which a small screen does not load.',
    },
    'Left: the file as it is on the system now. Right: your text.': {de: 'Links: die Datei, wie sie jetzt auf dem System steht. Rechts: Ihr Text.', en: 'Left: the file as it is on the system now. Right: your text.'},
    'Left: the backup of {time}. Right: the file as it is in the editor.': {de: 'Links: die Sicherung von {time}. Rechts: die Datei, wie sie im Editor steht.', en: 'Left: the backup of {time}. Right: the file as it is in the editor.'},
    'Opened through a link: the file is {real}.': {de: 'Über einen Link geöffnet: Die Datei ist {real}.', en: 'Opened through a link: the file is {real}.'},
    'Managed by the system: it may overwrite or refuse a change here.': {
        de: 'Vom System verwaltet: Es kann eine Änderung hier überschreiben oder ablehnen.',
        en: 'Managed by the system: it may overwrite or refuse a change here.',
    },
    'Only the first part is shown.': {de: 'Gezeigt wird nur der erste Teil.', en: 'Only the first part is shown.'},
    'This file has both kinds of line ends. A save writes one kind throughout.': {
        de: 'Diese Datei hat beide Arten von Zeilenenden. Beim Speichern wird durchgehend eine Art geschrieben.',
        en: 'This file has both kinds of line ends. A save writes one kind throughout.',
    },
    '{name} has {size}. Files over 10 MB are not opened here. Logs are better read on the system\'s Log page.': {
        de: '{name} hat {size}. Dateien über 10 MB werden hier nicht geöffnet. Protokolle liest man besser auf der Protokollseite des Systems.',
        en: '{name} has {size}. Files over 10 MB are not opened here. Logs are better read on the system\'s Log page.',
    },
    'Unsaved changes': {de: 'Ungespeicherte Änderungen', en: 'Unsaved changes'},
    'The changes to {name} are not saved. Discard them?': {de: 'Die Änderungen an {name} sind nicht gespeichert. Verwerfen?', en: 'The changes to {name} are not saved. Discard them?'},
    'The changes to {name} are not saved. End the session anyway?': {
        de: 'Die Änderungen an {name} sind nicht gespeichert. Die Sitzung trotzdem beenden?',
        en: 'The changes to {name} are not saved. End the session anyway?',
    },
    'Discard': {de: 'Verwerfen', en: 'Discard'},

    // the files tab: new, rename, delete, upload
    'Name': {de: 'Name', en: 'Name'},
    'New path': {de: 'Neuer Pfad', en: 'New path'},
    'Create': {de: 'Anlegen', en: 'Create'},
    'A path in another folder moves it there. Nothing is overwritten.': {
        de: 'Ein Pfad in einem anderen Ordner verschiebt es dorthin. Überschrieben wird nichts.',
        en: 'A path in another folder moves it there. Nothing is overwritten.',
    },
    'Delete?': {de: 'Löschen?', en: 'Delete?'},
    '{name} is deleted. There is no way back.': {de: '{name} wird gelöscht. Das lässt sich nicht rückgängig machen.', en: '{name} is deleted. There is no way back.'},
    'The link {name} is deleted, not what it leads to.': {de: 'Der Link {name} wird gelöscht, nicht das, worauf er zeigt.', en: 'The link {name} is deleted, not what it leads to.'},
    'Delete the folder with everything in it?': {de: 'Den Ordner mit allem darin löschen?', en: 'Delete the folder with everything in it?'},
    '{name} holds {n} entries, and what is in them. Type the folder\'s name to delete it all. There is no way back.': {
        de: '{name} enthält {n} Einträge und was darin liegt. Geben Sie den Namen des Ordners ein, um alles zu löschen. Das lässt sich nicht rückgängig machen.',
        en: '{name} holds {n} entries, and what is in them. Type the folder\'s name to delete it all. There is no way back.',
    },
    'The folder\'s name': {de: 'Name des Ordners', en: 'The folder\'s name'},
    'Deleted.': {de: 'Gelöscht.', en: 'Deleted.'},
    'Uploading {name} ({i} of {n})': {de: 'Lade {name} hoch ({i} von {n})', en: 'Uploading {name} ({i} of {n})'},
    '{name} is over 64 MB and was not uploaded.': {de: '{name} ist größer als 64 MB und wurde nicht hochgeladen.', en: '{name} is over 64 MB and was not uploaded.'},
    'Replace the file?': {de: 'Die Datei ersetzen?', en: 'Replace the file?'},
    '{name} exists already. Its present version goes to the backups first (up to 50 MB).': {
        de: '{name} gibt es schon. Die jetzige Fassung kommt vorher in die Sicherungen (bis 50 MB).',
        en: '{name} exists already. Its present version goes to the backups first (up to 50 MB).',
    },
    'Replace': {de: 'Ersetzen', en: 'Replace'},
    '{name} was replaced without a backup: it was too large for one.': {
        de: '{name} wurde ohne Sicherung ersetzt: Die Datei war dafür zu groß.',
        en: '{name} was replaced without a backup: it was too large for one.',
    },
    '{n} files uploaded.': {de: '{n} Dateien hochgeladen.', en: '{n} files uploaded.'},

    // the files tab: properties and backups
    'Path': {de: 'Pfad', en: 'Path'},
    'Link to': {de: 'Link auf', en: 'Link to'},
    'broken': {de: 'defekt', en: 'broken'},
    'Size': {de: 'Größe', en: 'Size'},
    'Changed': {de: 'Geändert', en: 'Changed'},
    'Mode and owner': {de: 'Rechte und Besitzer', en: 'Mode and owner'},
    'Mode (octal)': {de: 'Rechte (oktal)', en: 'Mode (octal)'},
    'read': {de: 'lesen', en: 'read'},
    'write': {de: 'schreiben', en: 'write'},
    'execute': {de: 'ausführen', en: 'execute'},
    'Owner': {de: 'Besitzer', en: 'Owner'},
    'Group': {de: 'Gruppe', en: 'Group'},
    'Others': {de: 'Andere', en: 'Others'},
    'A mode is one to four digits from 0 to 7.': {de: 'Rechte sind eine bis vier Ziffern von 0 bis 7.', en: 'A mode is one to four digits from 0 to 7.'},
    'Applied to this one entry, never to what is in a folder.': {de: 'Gilt für diesen einen Eintrag, nie für den Inhalt eines Ordners.', en: 'Applied to this one entry, never to what is in a folder.'},
    'Backups': {de: 'Sicherungen', en: 'Backups'},
    'Every save keeps the version it replaces: the last {n} of each file, 50 MB in all, in {root}. They stay when the addon is uninstalled.': {
        de: 'Jedes Speichern bewahrt die Fassung auf, die es ersetzt: die letzten {n} jeder Datei, insgesamt 50 MB, in {root}. Sie bleiben, wenn das Addon deinstalliert wird.',
        en: 'Every save keeps the version it replaces: the last {n} of each file, 50 MB in all, in {root}. They stay when the addon is uninstalled.',
    },
    'No backup yet.': {de: 'Noch keine Sicherung.', en: 'No backup yet.'},
    'Open the file first.': {de: 'Öffnen Sie zuerst die Datei.', en: 'Open the file first.'},
    'Restore': {de: 'Wiederherstellen', en: 'Restore'},
    'Restore this version?': {de: 'Diese Fassung wiederherstellen?', en: 'Restore this version?'},
    '{name} is replaced by its version of {time}. The present version goes to the backups first.': {
        de: '{name} wird durch die Fassung von {time} ersetzt. Die jetzige Fassung kommt vorher in die Sicherungen.',
        en: '{name} is replaced by its version of {time}. The present version goes to the backups first.',
    },
    'Restored.': {de: 'Wiederhergestellt.', en: 'Restored.'},
    'Close': {de: 'Schließen', en: 'Close'},
    'Apply': {de: 'Anwenden', en: 'Apply'},
};
