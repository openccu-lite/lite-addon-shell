import {describe, expect, it} from 'vitest';

import {baseOf, decodeText, dirOf, encodeText, forTextarea, hexDump, joinPath, languageOf, managed, modeBits, modeLetters, sizeText, validMode, validName, validPath, withModeBit} from './text.js';

const bytes = (s: string) => new TextEncoder().encode(s);
const round = (raw: Uint8Array, edit: (t: string) => string = (t) => t) => {
    const d = decodeText(raw);
    return Array.from(encodeText(edit(d.text), d));
};

describe('a save changes only what the user changed', () => {
    it('keeps Unix line ends and the last line\'s end', () => {
        const raw = bytes('a\nb\n');
        expect(decodeText(raw)).toEqual({text: 'a\nb\n', bom: false, eol: 'lf', mixed: false});
        expect(round(raw)).toEqual(Array.from(raw));
    });
    it('keeps a file without a last line end as it is', () => {
        const raw = bytes('no newline at the end');
        expect(round(raw)).toEqual(Array.from(raw));
        expect(round(raw, (t) => t + '!')).toEqual(Array.from(bytes('no newline at the end!')));
    });
    it('keeps Windows line ends, also for lines an editor added with the other kind', () => {
        const raw = bytes('[s]\r\nk=v\r\n');
        expect(decodeText(raw).eol).toBe('crlf');
        expect(round(raw)).toEqual(Array.from(raw));
        expect(round(raw, (t) => t + 'new=1\n')).toEqual(Array.from(bytes('[s]\r\nk=v\r\nnew=1\r\n')));
        // a plain text field hands back Unix line ends only
        expect(round(raw, (t) => forTextarea(t) + 'x\n')).toEqual(Array.from(bytes('[s]\r\nk=v\r\nx\r\n')));
    });
    it('keeps a byte order mark and never shows it to the editor', () => {
        const raw = new Uint8Array([0xef, 0xbb, 0xbf, ...bytes('[s]\r\n')]);
        const d = decodeText(raw);
        expect(d.bom).toBe(true);
        expect(d.text).toBe('[s]\r\n');
        expect(round(raw)).toEqual(Array.from(raw));
        // and adds none to a file that has none
        expect(round(bytes('x\n'))).toEqual(Array.from(bytes('x\n')));
    });
    it('says when a file has both kinds of line ends, and writes the more frequent one', () => {
        const d = decodeText(bytes('a\r\nb\r\nc\n'));
        expect(d).toMatchObject({eol: 'crlf', mixed: true});
        expect(Array.from(encodeText(d.text, d))).toEqual(Array.from(bytes('a\r\nb\r\nc\r\n')));
        expect(decodeText(bytes('a\nb\nc\r\n'))).toMatchObject({eol: 'lf', mixed: true});
    });
    it('keeps umlauts, emoji and an empty file', () => {
        expect(round(bytes('Grüße 😀\n'))).toEqual(Array.from(bytes('Grüße 😀\n')));
        expect(round(new Uint8Array())).toEqual([]);
        expect(decodeText(new Uint8Array())).toEqual({text: '', bom: false, eol: 'lf', mixed: false});
    });
});

describe('the language of a file', () => {
    it('goes by the name', () => {
        const cases: Record<string, string> = {
            'rfd.conf': 'ini', 'hm_addons.cfg': 'ini', 'addon-shell.service': 'ini', 'settings.json': 'json', 'InterfacesList.xml': 'xml',
            'compose.yaml': 'yaml', 'a.yml': 'yaml', 'script.tcl': 'tcl', 'x.lua': 'lua', 'x.py': 'python', 'app.mjs': 'javascript', 'index.html': 'html',
            'app.css': 'css', 'README.md': 'markdown', 'Dockerfile': 'dockerfile', 'rc.d': 'shell', 'update_script': 'shell', 'install.sh': 'shell',
            'crontab': 'shell', 'notes.txt': 'plaintext', 'messages': 'plaintext', 'lighttpd.conf': 'ini', '.profile': 'shell', 'UPPER.JSON': 'json',
        };
        for (const [name, lang] of Object.entries(cases)) expect(languageOf(name), name).toBe(lang);
    });
    it('reads a script\'s first line when the name says nothing', () => {
        expect(languageOf('daemon', '#!/bin/sh')).toBe('shell');
        expect(languageOf('S50thing', '#!/usr/bin/env bash')).toBe('shell');
        expect(languageOf('api', '#!/bin/tclsh')).toBe('tcl');
        expect(languageOf('tool', '#!/usr/bin/python3')).toBe('python');
        expect(languageOf('tool', '#!/usr/bin/env node')).toBe('javascript');
        expect(languageOf('data', '<?xml version="1.0"?>')).toBe('xml');
        expect(languageOf('data', 'just text')).toBe('plaintext');
        // the interpreter wins over the ending: a .cgi may be a shell script
        expect(languageOf('status.cgi', '#!/bin/sh')).toBe('shell');
        expect(languageOf('status.cgi', '')).toBe('tcl');
    });
});

describe('what a listing shows', () => {
    it('sizes', () => {
        expect(sizeText(0)).toBe('0 B');
        expect(sizeText(1023)).toBe('1023 B');
        expect(sizeText(1024)).toBe('1.0 kB');
        expect(sizeText(4300)).toBe('4.2 kB');
        expect(sizeText(2 << 20)).toBe('2.0 MB');
        expect(sizeText(150 << 20)).toBe('150 MB');
        expect(sizeText(3 * 2 ** 30)).toBe('3.0 GB');
    });
    it('modes as letters, with the set-id and sticky bits', () => {
        expect(modeLetters('0644')).toBe('rw-r--r--');
        expect(modeLetters('0755')).toBe('rwxr-xr-x');
        expect(modeLetters('4755')).toBe('rwsr-xr-x');
        expect(modeLetters('4644')).toBe('rwSr--r--');
        expect(modeLetters('2775')).toBe('rwxrwsr-x');
        expect(modeLetters('1777')).toBe('rwxrwxrwt');
        expect(modeLetters('1776')).toBe('rwxrwxrwT');
        expect(modeLetters('0000')).toBe('---------');
    });
    it('modes as nine boxes, and back, keeping the first digit', () => {
        expect(modeBits('0640')).toEqual([true, true, false, true, false, false, false, false, false]);
        expect(withModeBit('0640', 6, true)).toBe('0644');
        expect(withModeBit('0644', 1, false)).toBe('0444');
        expect(withModeBit('4755', 2, false)).toBe('4655');
        expect(validMode('0644')).toBe(true);
        expect(validMode('755')).toBe(true);
        expect(validMode('0888')).toBe(false);
        expect(validMode('12345')).toBe(false);
        expect(validMode('')).toBe(false);
    });
    it('a hex dump of sixteen bytes a line', () => {
        const dump = hexDump(new Uint8Array([0x7f, 0x45, 0x4c, 0x46, 0, 1, 2, 3, 0x62, 0x69, 0x6e, 0x61, 0x72, 0x79, 0, 0xff, 0xfe]));
        expect(dump.split('\n')).toEqual([
            '00000000  7f 45 4c 46 00 01 02 03  62 69 6e 61 72 79 00 ff  .ELF....binary..',
            '00000010  fe                                                .',
        ]);
        expect(hexDump(new Uint8Array())).toBe('');
    });
});

describe('paths and names', () => {
    it('splits and joins', () => {
        expect(dirOf('/usr/local/etc')).toBe('/usr/local');
        expect(dirOf('/usr')).toBe('/');
        expect(dirOf('/')).toBe('/');
        expect(baseOf('/usr/local/etc')).toBe('etc');
        expect(baseOf('/')).toBe('/');
        expect(joinPath('/', 'usr')).toBe('/usr');
        expect(joinPath('/usr', 'local')).toBe('/usr/local');
    });
    it('a name is one element', () => {
        for (const ok of ['a', 'rfd.conf', 'with space', '.hidden', 'ä😀', '...']) expect(validName(ok), ok).toBe(true);
        for (const bad of ['', '.', '..', 'a/b', '/a', 'a\nb', 'a\u0000', 'x'.repeat(256)]) expect(validName(bad), JSON.stringify(bad)).toBe(false);
        expect(validPath('/usr/local/x y')).toBe(true);
        expect(validPath('usr/local')).toBe(false);
        expect(validPath('/a\nb')).toBe(false);
    });
    it('knows the trees the system writes itself', () => {
        expect(managed('/usr/local/etc/config/addon-policy/shell')).toBe(true);
        expect(managed('/usr/local/etc/config/addon-policy')).toBe(true);
        expect(managed('/usr/local/etc/config/addon-policy-notes')).toBe(false);
        expect(managed('/usr/local/etc/config/rfd.conf')).toBe(false);
    });
});
