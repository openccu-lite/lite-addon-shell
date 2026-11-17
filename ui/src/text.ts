// What the files page knows about a file's bytes, apart from any editor: how a text is decoded
// and encoded again so that a save changes only what the user changed (the byte order mark, the
// line ends and the last line's end stay as they were), which language colours a file, how bytes
// read as a hex dump, how sizes and modes read. Pure functions; the components do the rest.

export type Eol = 'lf' | 'crlf';

export interface Decoded {
    /** The text as an editor holds it: no byte order mark, line ends as they are in the file. */
    text: string;
    /** The file starts with a UTF-8 byte order mark. */
    bom: boolean;
    /** The line end a save writes: the file's own, or the more frequent one of a file with both. */
    eol: Eol;
    /** The file has both kinds of line ends: a save makes them one kind. */
    mixed: boolean;
}

const BOM = [0xef, 0xbb, 0xbf];

/** Decodes a UTF-8 file. The server has already said it is text. */
export function decodeText(bytes: Uint8Array): Decoded {
    const bom = bytes.length >= 3 && bytes[0] === BOM[0] && bytes[1] === BOM[1] && bytes[2] === BOM[2];
    const text = new TextDecoder('utf-8', {ignoreBOM: true}).decode(bom ? bytes.subarray(3) : bytes);
    let crlf = 0;
    let lf = 0;
    for (let i = text.indexOf('\n'); i >= 0; i = text.indexOf('\n', i + 1)) {
        if (i > 0 && text[i - 1] === '\r') crlf++;
        else lf++;
    }
    return {text, bom, eol: crlf > lf ? 'crlf' : 'lf', mixed: crlf > 0 && lf > 0};
}

/**
 * Encodes an editor's text for a save: the file's line ends (an editor may hand back either
 * kind), its byte order mark if it had one. Nothing is added at the end and nothing removed: a
 * file without a last line end stays one, unless the user typed it.
 */
export function encodeText(text: string, was: Pick<Decoded, 'bom' | 'eol'>): Uint8Array {
    const lf = text.replace(/\r\n/g, '\n');
    const body = new TextEncoder().encode(was.eol === 'crlf' ? lf.replace(/\n/g, '\r\n') : lf);
    if (!was.bom) return body;
    const out = new Uint8Array(body.length + 3);
    out.set(BOM);
    out.set(body, 3);
    return out;
}

/** What a plain text field holds: it knows one kind of line end. */
export function forTextarea(text: string): string {
    return text.replace(/\r\n/g, '\n');
}

const BY_NAME: Record<string, string> = {
    'dockerfile': 'dockerfile',
    'containerfile': 'dockerfile',
    'rc.d': 'shell',
    'update_script': 'shell',
    'crontab': 'shell',
    '.profile': 'shell',
    '.bashrc': 'shell',
    'profile': 'shell',
    'passwd': 'plaintext',
};

const BY_EXT: Record<string, string> = {
    sh: 'shell', bash: 'shell', ash: 'shell',
    json: 'json', map: 'json',
    ini: 'ini', conf: 'ini', cfg: 'ini', cnf: 'ini', properties: 'ini', env: 'ini',
    service: 'ini', timer: 'ini', socket: 'ini', mount: 'ini', target: 'ini', path: 'ini', network: 'ini', netdev: 'ini', link: 'ini', rules: 'ini',
    xml: 'xml', xsl: 'xml', svg: 'xml',
    yaml: 'yaml', yml: 'yaml',
    tcl: 'tcl', cgi: 'tcl',
    lua: 'lua',
    py: 'python',
    js: 'javascript', mjs: 'javascript', cjs: 'javascript',
    html: 'html', htm: 'html',
    css: 'css',
    md: 'markdown', markdown: 'markdown',
};

/** The languages the editor colours; everything else is plain text. */
export const LANGUAGES = ['shell', 'json', 'ini', 'xml', 'yaml', 'tcl', 'lua', 'python', 'javascript', 'html', 'css', 'markdown', 'dockerfile', 'plaintext'] as const;

/** The language of a file, by its name, then by its first line (a script's interpreter). */
export function languageOf(name: string, firstLine = ''): string {
    const lower = name.toLowerCase();
    if (BY_NAME[lower]) return BY_NAME[lower];
    if (lower.startsWith('dockerfile.')) return 'dockerfile';
    const dot = lower.lastIndexOf('.');
    const ext = dot > 0 ? lower.slice(dot + 1) : '';
    const m = /^#!\s*\S*?(?:\/|env\s+)?(sh|bash|ash|dash|tclsh|python[0-9.]*|lua|node)\b/.exec(firstLine);
    if (m) {
        const i = m[1]!;
        if (i === 'tclsh') return 'tcl';
        if (i.startsWith('python')) return 'python';
        if (i === 'lua') return 'lua';
        if (i === 'node') return 'javascript';
        return 'shell';
    }
    if (ext && BY_EXT[ext]) return BY_EXT[ext];
    if (/^<\?xml\b/.test(firstLine)) return 'xml';
    return 'plaintext';
}

/** A hex dump, sixteen bytes a line: offset, the bytes, what they read as. */
export function hexDump(bytes: Uint8Array): string {
    const lines: string[] = [];
    for (let off = 0; off < bytes.length; off += 16) {
        const row = bytes.subarray(off, off + 16);
        let hex = '';
        let text = '';
        for (let i = 0; i < 16; i++) {
            const b = row[i];
            hex += (b === undefined ? '  ' : b.toString(16).padStart(2, '0')) + (i === 7 ? '  ' : ' ');
            if (b !== undefined) text += b >= 0x20 && b < 0x7f ? String.fromCharCode(b) : '.';
        }
        lines.push(off.toString(16).padStart(8, '0') + '  ' + hex + ' ' + text);
    }
    return lines.join('\n');
}

/** 812 B, 4.2 kB, 1.5 MB: a size as people read it (powers of 1024, one decimal). */
export function sizeText(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    const units = ['kB', 'MB', 'GB', 'TB'];
    let v = bytes / 1024;
    let u = 0;
    while (v >= 1024 && u < units.length - 1) {
        v /= 1024;
        u++;
    }
    return `${v >= 100 ? Math.round(v) : v.toFixed(1)} ${units[u]}`;
}

/** rw-r--r-- of the last three octal digits; the first digit's bits show as s, S, t, T. */
export function modeLetters(mode: string): string {
    const n = parseInt(mode, 8) || 0;
    const bit = (b: number, c: string) => (n & b ? c : '-');
    const special = (x: number, s: number, low: string) => (n & s ? (n & x ? low : low.toUpperCase()) : n & x ? 'x' : '-');
    return (
        bit(0o400, 'r') + bit(0o200, 'w') + special(0o100, 0o4000, 's') +
        bit(0o040, 'r') + bit(0o020, 'w') + special(0o010, 0o2000, 's') +
        bit(0o004, 'r') + bit(0o002, 'w') + special(0o001, 0o1000, 't')
    );
}

/** The nine permission bits of a mode, most significant first (user r, w, x, group ..., other ...). */
export function modeBits(mode: string): boolean[] {
    const n = parseInt(mode, 8) || 0;
    return [0o400, 0o200, 0o100, 0o040, 0o020, 0o010, 0o004, 0o002, 0o001].map((b) => (n & b) !== 0);
}

/** A mode with one of the nine bits set or cleared; the first digit (set-id, sticky) stays. */
export function withModeBit(mode: string, index: number, on: boolean): string {
    const n = parseInt(mode, 8) || 0;
    const b = [0o400, 0o200, 0o100, 0o040, 0o020, 0o010, 0o004, 0o002, 0o001][index] ?? 0;
    return (on ? n | b : n & ~b).toString(8).padStart(4, '0');
}

/** Whether a mode is one to four octal digits. */
export function validMode(mode: string): boolean {
    return /^[0-7]{1,4}$/.test(mode);
}

/** A path's directory and its last element. */
export function dirOf(path: string): string {
    const i = path.lastIndexOf('/');
    return i <= 0 ? '/' : path.slice(0, i);
}

export function baseOf(path: string): string {
    return path === '/' ? '/' : path.slice(path.lastIndexOf('/') + 1);
}

export function joinPath(dir: string, name: string): string {
    return (dir === '/' ? '' : dir) + '/' + name;
}

/** Whether a name can be a file's or a folder's: one element, nothing a path means something by. */
export function validName(name: string): boolean {
    // eslint-disable-next-line no-control-regex
    return name !== '' && name !== '.' && name !== '..' && !/[/\u0000-\u001f\u007f]/.test(name) && new TextEncoder().encode(name).length <= 255;
}

/** Whether a path is one the files page sends: absolute, no control character. */
export function validPath(path: string): boolean {
    // eslint-disable-next-line no-control-regex
    return path.startsWith('/') && !/[\u0000-\u001f\u007f]/.test(path) && path.length <= 4096;
}

/** Trees the system writes itself: an edit there is overwritten or refused by the system. */
export const MANAGED = ['/usr/local/etc/config/addon-policy', '/usr/local/etc/occulite'];

export function managed(path: string): boolean {
    return MANAGED.some((m) => path === m || path.startsWith(m + '/'));
}
