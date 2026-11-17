import {describe, expect, it} from 'vitest';

import {addonPill, blockReason, clock, countdown, END_REASONS, endReason, pasteLines, settingsEqual, settingsValid, signInError, sshPill, transportPill, windowLabel} from './model.js';
import type {Settings, State} from './model.js';

const settings: Settings = {idle_minutes: 15, max_hours: 8, max_sessions: 2, allow_tunnel: false, files_read_only: false};
const bounds = {min: {...settings, idle_minutes: 5, max_hours: 1, max_sessions: 1}, max: {...settings, idle_minutes: 60, max_hours: 12, max_sessions: 4}};
const base: State = {
    version: '0.1.0', now: Date.UTC(2026, 10, 17, 12, 0, 0), user: 'admin', enabled: true, ssh: 'on', transport: 'https', blocked: '', auth_off: false,
    settings, defaults: settings, bounds, windows: [1, 8, 0], window: 8, sessions: [],
};

describe('the pills', () => {
    it('the addon: off is neutral, on is a caution and never the ok colour', () => {
        expect(addonPill({enabled: false, now: base.now})).toEqual({label: 'Off', tone: 'idle'});
        expect(addonPill({enabled: true, now: base.now})).toEqual({label: 'On', tone: 'warn'});
        const p = addonPill({enabled: true, until: base.now + 3600_000, now: base.now}, 'de');
        expect(p.label).toBe('On until {time}');
        expect(p.tone).toBe('warn');
        expect(p.values?.time).toMatch(/^\d\d:\d\d$/);
    });
    it('SSH and the connection', () => {
        expect(sshPill('on').tone).toBe('ok');
        expect(sshPill('off')).toEqual({label: 'Off', tone: 'bad'});
        expect(sshPill('keys-only')).toEqual({label: 'Keys only', tone: 'bad'});
        expect(sshPill('unknown').label).toBe('Unknown');
        expect(transportPill({transport: 'https', blocked: ''})).toEqual({label: 'HTTPS', tone: 'ok'});
        expect(transportPill({transport: 'http', blocked: 'insecure-transport'})).toEqual({label: 'HTTP', tone: 'bad'});
        expect(transportPill({transport: 'tunnel', blocked: 'tunnel-refused'})).toEqual({label: 'Tunnel', tone: 'bad'});
        expect(transportPill({transport: 'tunnel', blocked: ''})).toEqual({label: 'Tunnel', tone: 'warn'});
    });
});

describe('why the shell is blocked', () => {
    it('nothing blocks an enabled addon over HTTPS with SSH on', () => {
        expect(blockReason(base)).toBe('');
    });
    it('names the first thing the user can change', () => {
        expect(blockReason({...base, enabled: false})).toMatch(/switched off/);
        expect(blockReason({...base, enabled: false, auth_off: true})).toMatch(/without a login/);
        expect(blockReason({...base, blocked: 'insecure-transport'})).toMatch(/HTTPS only/);
        expect(blockReason({...base, blocked: 'tunnel-refused'})).toMatch(/tunnel addon/);
        expect(blockReason({...base, ssh: 'off'})).toMatch(/SSH is switched off/);
        expect(blockReason({...base, ssh: 'keys-only'})).toMatch(/keys only/);
        expect(blockReason({...base, ssh: 'unknown'})).toMatch(/system's own/);
        const s = {id: '1', kind: 'terminal', user: 'a', client: 'c', since: 0, last_input: 0, idle_ends: 0, ends: 0, attached: true, mine: true};
        expect(blockReason({...base, sessions: [s, {...s, id: '2'}]})).toMatch(/Too many sessions/);
        expect(blockReason({...base, sessions: [s]})).toBe('');
    });
});

describe('times', () => {
    it('a countdown never goes below zero and keeps two digits', () => {
        expect(countdown(15 * 60_000)).toBe('15:00');
        expect(countdown(59_001)).toBe('01:00');
        expect(countdown(999)).toBe('00:01');
        expect(countdown(0)).toBe('00:00');
        expect(countdown(-5000)).toBe('00:00');
        expect(countdown(8 * 3600_000)).toBe('8:00:00');
    });
    it('the clock is 24 hours in both languages, with the day when it is not today', () => {
        const now = new Date(2026, 10, 17, 12, 0).getTime();
        const evening = new Date(2026, 10, 17, 20, 37).getTime();
        expect(clock(evening, now, 'en')).toBe('20:37');
        expect(clock(evening, now, 'de')).toBe('20:37');
        const tomorrow = new Date(2026, 10, 18, 4, 37).getTime();
        expect(clock(tomorrow, now, 'en')).toBe('18/11 04:37');
        expect(clock(tomorrow, now, 'de')).toBe('18.11. 04:37');
    });
    it('the switch lengths', () => {
        expect(windowLabel(1)).toEqual({key: '1 hour'});
        expect(windowLabel(8)).toEqual({key: '{n} hours', values: {n: '8'}});
        expect(windowLabel(0)).toEqual({key: 'until I switch it off'});
    });
});

describe('the sign-in errors', () => {
    it('a wrong password says the tries left, the last one the lock', () => {
        expect(signInError('wrong-password', {tries_left: 3}, 0)).toEqual({key: 'Wrong password. {n} tries left.', values: {n: '3'}});
        const last = signInError('wrong-password', {tries_left: 0, until: new Date(2026, 10, 17, 14, 32).getTime()}, new Date(2026, 10, 17, 14, 17).getTime(), 'en');
        expect(last.key).toMatch(/Too many failed attempts/);
        expect(last.values?.time).toBe('14:32');
        expect(signInError('locked', {until: new Date(2026, 10, 17, 14, 32).getTime()}, new Date(2026, 10, 17, 14, 17).getTime(), 'de').values?.time).toBe('14:32');
    });
    it('every code the backend names has its own sentence', () => {
        const fallback = signInError('nonsense', undefined, 0).key;
        for (const code of ['too-many-sessions', 'ssh-off', 'keys-only', 'disabled', 'insecure-transport', 'tunnel-refused', 'auth-off', 'host-key', 'forbidden']) {
            expect(signInError(code, undefined, 0).key, code).not.toBe(fallback);
        }
        expect(signInError('ssh', undefined, 0).key).toBe(fallback);
    });
});

describe('ends and pastes', () => {
    it('an unknown reason reads as a lost connection', () => {
        expect(endReason('idle')).toBe('end.idle');
        expect(endReason('whatever')).toBe('end.lost');
        expect(END_REASONS).toContain('signed-out');
    });
    it('counts the lines of a paste', () => {
        expect(pasteLines('')).toBe(0);
        expect(pasteLines('ls -l')).toBe(1);
        expect(pasteLines('ls -l\n')).toBe(1);
        expect(pasteLines('a\r\nb\r\nc')).toBe(3);
        expect(pasteLines('rm -rf /tmp/x\nreboot\n')).toBe(2);
    });
});

describe('the settings form', () => {
    it('is valid within the bounds only', () => {
        expect(settingsValid(settings, bounds)).toBe(true);
        expect(settingsValid({...settings, idle_minutes: 4}, bounds)).toBe(false);
        expect(settingsValid({...settings, max_hours: 13}, bounds)).toBe(false);
        expect(settingsValid({...settings, max_sessions: 2.5}, bounds)).toBe(false);
        expect(settingsEqual(settings, {...settings})).toBe(true);
        expect(settingsEqual(settings, {...settings, allow_tunnel: true})).toBe(false);
        expect(settingsEqual(settings, {...settings, files_read_only: true})).toBe(false);
    });
});
