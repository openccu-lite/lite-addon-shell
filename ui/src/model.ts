// What the backend answers, and the pure helpers the two pages share: which pill says what, why a
// button is blocked, how a countdown and an end reason read. Every text is a message key; the
// components translate.

export interface Settings {
    idle_minutes: number;
    max_hours: number;
    max_sessions: number;
    allow_tunnel: boolean;
    files_read_only: boolean;
}

export interface Session {
    id: string;
    kind: string;
    user: string;
    client: string;
    since: number;
    last_input: number;
    idle_ends: number;
    ends: number;
    attached: boolean;
    mine: boolean;
}

export type SSHState = 'on' | 'off' | 'keys-only' | 'refusing' | 'unknown';
export type Transport = 'https' | 'http' | 'tunnel';

export interface State {
    version: string;
    now: number;
    user: string;
    enabled: boolean;
    until?: number;
    by?: string;
    ssh: SSHState;
    transport: Transport;
    blocked: '' | 'insecure-transport' | 'tunnel-refused';
    auth_off: boolean;
    peek?: boolean;
    settings: Settings;
    defaults: Settings;
    bounds: {min: Settings; max: Settings};
    windows: number[];
    window: number;
    sessions: Session[];
    locked_until?: number;
}

export interface AuditEntry {
    at: number;
    line: string;
}

export type Tone = 'ok' | 'warn' | 'bad' | 'idle';

export interface Pill {
    /** message key */
    label: string;
    /** values for the label's placeholders */
    values?: Record<string, string>;
    tone: Tone;
}

/**
 * HH:MM of a time today, with the day and month in front when it is another day. 24 hours and
 * day before month in both languages: "en" alone would be the American 12-hour clock.
 */
export function clock(at: number, now: number, language?: string): string {
    const locale = language === 'en' ? 'en-GB' : language;
    const d = new Date(at);
    const time = d.toLocaleTimeString(locale, {hour: '2-digit', minute: '2-digit', hourCycle: 'h23'});
    if (new Date(now).toDateString() === d.toDateString()) return time;
    return d.toLocaleDateString(locale, {day: '2-digit', month: '2-digit'}) + ' ' + time;
}

/** What the pages say while the SSH server makes the system wait after failed sign-ins. */
export const REFUSING = 'The SSH server takes no sign-in from the system for a moment: after several failed sign-ins it makes everyone wait. Try again in a minute.';

/** The addon's pill: a root shell that is on is a caution, never the ok colour. */
export function addonPill(s: Pick<State, 'enabled' | 'until' | 'now'>, locale?: string): Pill {
    if (!s.enabled) return {label: 'Off', tone: 'idle'};
    if (s.until === undefined) return {label: 'On', tone: 'warn'};
    return {label: 'On until {time}', values: {time: clock(s.until, s.now, locale)}, tone: 'warn'};
}

export function sshPill(ssh: SSHState): Pill {
    switch (ssh) {
        case 'on':
            return {label: 'On', tone: 'ok'};
        case 'off':
            return {label: 'Off', tone: 'bad'};
        case 'keys-only':
            return {label: 'Keys only', tone: 'bad'};
        case 'refusing':
            return {label: 'Not now', tone: 'bad'};
        default:
            return {label: 'Unknown', tone: 'bad'};
    }
}

export function transportPill(s: Pick<State, 'transport' | 'blocked'>): Pill {
    if (s.transport === 'https') return {label: 'HTTPS', tone: 'ok'};
    if (s.transport === 'tunnel') return {label: 'Tunnel', tone: s.blocked ? 'bad' : 'warn'};
    return {label: 'HTTP', tone: s.blocked ? 'bad' : 'warn'};
}

/**
 * Why a shell cannot be opened right now, as a message key; '' when it can. The order is the
 * order in which the user can do something about it.
 */
export function blockReason(s: Pick<State, 'enabled' | 'ssh' | 'blocked' | 'auth_off' | 'sessions' | 'settings'>): string {
    if (s.auth_off) return 'The system runs without a login: every visitor would be an administrator. The shell stays off.';
    if (!s.enabled) return 'The addon is switched off. Switch it on first.';
    if (s.blocked === 'insecure-transport') return 'The shell works over HTTPS only. Open the system with https:// in the address.';
    if (s.blocked === 'tunnel-refused') return 'The shell is not offered through a tunnel addon. An administrator can allow it in the settings below.';
    if (s.ssh === 'off') return 'SSH is switched off. Switch it on under System → Remote access.';
    if (s.ssh === 'keys-only') return 'SSH accepts keys only, so root\'s password does not work. Allow password login under System → Remote access.';
    if (s.ssh === 'refusing') return REFUSING;
    if (s.ssh === 'unknown') return 'The SSH server did not answer as the system\'s own. The shell stays closed.';
    if (s.sessions.length >= s.settings.max_sessions) return 'Too many sessions are open. End one first.';
    return '';
}

/** mm:ss (or h:mm:ss) of a remaining time; never negative. Fixed-width digits are the CSS's job. */
export function countdown(ms: number): string {
    const s = Math.max(0, Math.ceil(ms / 1000));
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    const sec = s % 60;
    const two = (n: number) => String(n).padStart(2, '0');
    return h > 0 ? `${h}:${two(m)}:${two(sec)}` : `${two(m)}:${two(sec)}`;
}

/** The end reasons the backend names; an unknown one reads as a lost connection. */
export const END_REASONS = ['exit', 'idle', 'max', 'signed-out', 'ended', 'addon-off', 'ssh-off', 'tab-closed', 'connection', 'stopping', 'unverified', 'stuck', 'replaced', 'lost'] as const;

export function endReason(reason: string): string {
    return 'end.' + ((END_REASONS as readonly string[]).includes(reason) ? reason : 'lost');
}

/** The sign-in errors the backend names, as message keys with their values. */
export function signInError(code: string, body: {tries_left?: number; until?: number} | undefined, now: number, locale?: string): {key: string; values?: Record<string, string>} {
    switch (code) {
        case 'wrong-password':
            if (body?.tries_left === 0 && body.until) return {key: 'Wrong password. Too many failed attempts: try again at {time}.', values: {time: clock(body.until, now, locale)}};
            return {key: 'Wrong password. {n} tries left.', values: {n: String(body?.tries_left ?? 0)}};
        case 'locked':
            return {key: 'Too many failed attempts. Try again at {time}.', values: {time: body?.until ? clock(body.until, now, locale) : '?'}};
        case 'too-many-sessions':
            return {key: 'Too many sessions are open. End one first.'};
        case 'ssh-off':
            return {key: 'SSH is switched off. Switch it on under System → Remote access.'};
        case 'keys-only':
            return {key: 'SSH accepts keys only, so root\'s password does not work. Allow password login under System → Remote access.'};
        case 'disabled':
            return {key: 'The addon is switched off. Switch it on first.'};
        case 'insecure-transport':
            return {key: 'The shell works over HTTPS only. Open the system with https:// in the address.'};
        case 'tunnel-refused':
            return {key: 'The shell is not offered through a tunnel addon. An administrator can allow it in the settings below.'};
        case 'auth-off':
            return {key: 'The system runs without a login: every visitor would be an administrator. The shell stays off.'};
        case 'ssh-refusing':
            return {key: REFUSING};
        case 'host-key':
            return {key: 'The SSH server did not answer as the system\'s own. The shell stays closed.'};
        case 'forbidden':
        case 'session-invalid':
        case '401':
            return {key: 'You are not signed in to the system as an administrator (any more). Sign in again.'};
        default:
            return {key: 'The sign-in failed. The addon\'s log says why.'};
    }
}

/** How many lines a paste holds: more than one asks first. */
export function pasteLines(text: string): number {
    const t = text.replace(/\r\n?/g, '\n').replace(/\n+$/, '');
    return t === '' ? 0 : t.split('\n').length;
}

/** The label of a switch length in hours (0: until switched off), as a message key with values. */
export function windowLabel(hours: number): {key: string; values?: Record<string, string>} {
    if (hours === 0) return {key: 'until I switch it off'};
    if (hours === 1) return {key: '1 hour'};
    return {key: '{n} hours', values: {n: String(hours)}};
}

/** Whether the settings form differs from what is stored, and whether it is within the bounds. */
export function settingsValid(s: Settings, b: {min: Settings; max: Settings}): boolean {
    const within = (v: number, lo: number, hi: number) => Number.isInteger(v) && v >= lo && v <= hi;
    return within(s.idle_minutes, b.min.idle_minutes, b.max.idle_minutes) && within(s.max_hours, b.min.max_hours, b.max.max_hours) && within(s.max_sessions, b.min.max_sessions, b.max.max_sessions);
}

export function settingsEqual(a: Settings, b: Settings): boolean {
    return a.idle_minutes === b.idle_minutes && a.max_hours === b.max_hours && a.max_sessions === b.max_sessions && a.allow_tunnel === b.allow_tunnel && a.files_read_only === b.files_read_only;
}
