// The files of a files session, as the page asks for them. Every request carries the session's
// handle in a header - never in the address, where a history, a log or a Referer would keep it -
// and the kit's CSRF header when it changes something. A download cannot carry a header (the
// browser fetches it), so the page asks for a ticket first: a random name for one download
// within half a minute.

import {ApiError, apiUrl} from './api.js';

export type EntryType = 'file' | 'dir' | 'link' | 'device' | 'fifo' | 'socket' | 'other';
/** Why something cannot be changed: a read-only mount, the kernel's files, the backups, the addon's setting. */
export type Reason = '' | 'read-only' | 'kernel' | 'backups' | 'setting';

export interface Entry {
    name: string;
    type: EntryType;
    size: number;
    mode: string;
    uid: number;
    gid: number;
    owner: string;
    group: string;
    mtime: number;
    target?: string;
    target_type?: EntryType;
    locked?: Reason;
}

export interface Place {
    writable: boolean;
    reason: Reason;
    mount: string;
}

export interface Listing extends Place {
    path: string;
    real: string;
    entries: Entry[];
    truncated: boolean;
    files_read_only: boolean;
}

export interface Info extends Entry, Place {
    path: string;
    real: string;
}

export interface FileMeta extends Info {
    kind: 'text' | 'binary';
    sha256: string;
    truncated: boolean;
    editable: boolean;
    /** Why the editor may not save it: binary, large, or a Reason. */
    why: string;
}

export interface Saved {
    path: string;
    real: string;
    size: number;
    mtime: number;
    mode: string;
    sha256?: string;
    backup: string;
    backup_skipped?: string;
    in_place: boolean;
    created: boolean;
}

export interface Backup {
    name: string;
    path: string;
    at: number;
    size: number;
}

export interface Id {
    name: string;
    id: number;
}

/** The largest upload, and the largest file the editor saves; the server holds the same limits. */
export const MAX_UPLOAD = 64 << 20;
export const MAX_EDIT = 2 << 20;

const q = (path: string) => 'path=' + encodeURIComponent(path);

export class FilesApi {
    /**
     * @param id the session
     * @param handle the session's secret; it lives in this object and in the tab's storage only
     * @param used called at every request: a request is the user's doing and resets the idle time
     */
    constructor(
        readonly id: string,
        private readonly handle: string,
        private readonly used: () => void = () => {},
    ) {}

    private url(call: string): string {
        return apiUrl(`api/v1/sessions/${encodeURIComponent(this.id)}/fs/${call}`);
    }

    private async request(method: string, call: string, body?: BodyInit | null, headers: Record<string, string> = {}): Promise<Response> {
        this.used();
        const h: Record<string, string> = {'Accept': 'application/json', 'X-Shell-Handle': this.handle, ...headers};
        if (method !== 'GET') h['X-CCU-Addon-Request'] = '1';
        const res = await fetch(this.url(call), {method, headers: h, body, credentials: 'same-origin', cache: 'no-store'});
        if (!res.ok) {
            let parsed: {error?: string; message?: string} = {};
            try {
                parsed = (await res.json()) as typeof parsed;
            } catch {
                /* not JSON */
            }
            throw new ApiError(res.status, parsed.error ?? String(res.status), parsed.message ?? res.statusText, parsed);
        }
        return res;
    }

    private async json<T>(method: string, call: string, body?: unknown): Promise<T> {
        const res = await this.request(method, call, body === undefined ? undefined : JSON.stringify(body), body === undefined ? {} : {'Content-Type': 'application/json'});
        return (await res.json()) as T;
    }

    list(path: string): Promise<Listing> {
        return this.json('GET', 'list?' + q(path));
    }

    stat(path: string): Promise<Info> {
        return this.json('GET', 'stat?' + q(path));
    }

    ids(): Promise<{users: Id[]; groups: Id[]}> {
        return this.json('GET', 'ids');
    }

    /** A file for the editor: its description and its bytes (a binary file's first 64 KiB). */
    async read(path: string): Promise<{meta: FileMeta; bytes: Uint8Array}> {
        const res = await this.request('GET', 'read?' + q(path));
        const meta = JSON.parse(res.headers.get('X-Shell-File') ?? '{}') as FileMeta;
        return {meta, bytes: new Uint8Array(await res.arrayBuffer())};
    }

    /** Saves over the file whose content had the SHA-256 `replaces`; 409 `conflict` when it is something else by now. */
    async write(path: string, bytes: Uint8Array, replaces: string): Promise<Saved> {
        const res = await this.request('PUT', 'write?' + q(path), bytes as BodyInit, {'Content-Type': 'application/octet-stream', 'If-Match': `"${replaces}"`});
        return (await res.json()) as Saved;
    }

    create(path: string): Promise<Saved> {
        return this.json('POST', 'create', {path});
    }

    mkdir(path: string): Promise<{ok: boolean}> {
        return this.json('POST', 'mkdir', {path});
    }

    rename(from: string, to: string): Promise<{ok: boolean}> {
        return this.json('POST', 'rename', {from, to});
    }

    remove(path: string, recursive = false): Promise<{removed: number}> {
        return this.json('POST', 'remove', {path, recursive});
    }

    chmod(path: string, mode: string): Promise<{ok: boolean}> {
        return this.json('POST', 'chmod', {path, mode});
    }

    chown(path: string, uid: number, gid: number): Promise<{ok: boolean}> {
        return this.json('POST', 'chown', {path, uid, gid});
    }

    /** Stores a file the user chose; the browser streams it from the disk. */
    async upload(path: string, file: Blob, overwrite: boolean): Promise<Saved> {
        const res = await this.request('POST', 'upload?' + q(path) + (overwrite ? '&overwrite=1' : ''), file, {'Content-Type': 'application/octet-stream'});
        return (await res.json()) as Saved;
    }

    backups(path: string): Promise<{backups: Backup[]; keep: number; keep_bytes: number; root: string}> {
        return this.json('GET', 'backups?' + q(path));
    }

    restore(path: string, name: string): Promise<Saved> {
        return this.json('POST', 'restore', {path, name});
    }

    /** The address of one download of the file: good once, for half a minute. */
    async downloadUrl(path: string): Promise<{url: string; name: string; size: number}> {
        const t = await this.json<{ticket: string; name: string; size: number}>('POST', 'download', {path});
        return {url: this.url('download/' + encodeURIComponent(t.ticket)), name: t.name, size: t.size};
    }
}

/** The message key for why something cannot be changed. */
export function reasonText(reason: string): string {
    switch (reason) {
        case 'read-only':
            return 'This part of the system is read-only. A change here would not survive the next system update. Settings live under /usr/local/etc/config.';
        case 'kernel':
            return 'These files belong to the kernel and are not changed here.';
        case 'backups':
            return 'The backups are written by saves only. They can be compared, restored and deleted.';
        case 'setting':
            return 'The files page is set to read-only in the addon\'s settings.';
        case 'binary':
            return 'This is not a text file. Its first bytes are shown; download it to work on it.';
        case 'large':
            return 'Files over 2 MB are shown, not edited. Download the file to work on it.';
        default:
            return '';
    }
}

/** The message key for a refusal of the file API, with its values. */
export function fsError(e: unknown): {key: string; values?: Record<string, string>} {
    const code = e instanceof ApiError ? e.code : '';
    switch (code) {
        case 'not-found':
            return {key: 'No such file or folder (any more).'};
        case 'permission-denied':
            return {key: 'Permission denied.'};
        case 'read-only':
        case 'kernel':
        case 'backups':
            return {key: reasonText(code)};
        case 'read-only-setting':
            return {key: reasonText('setting')};
        case 'no-space':
            return {key: 'No space left on the file system.'};
        case 'exists':
            return {key: 'That name is taken.'};
        case 'too-large':
            return {key: 'The file is too large.'};
        case 'special':
            return {key: 'Devices, pipes and sockets are not opened.'};
        case 'not-a-file':
        case 'not-a-directory':
            return {key: 'That is not what it was a moment ago. Refresh the folder.'};
        case 'is-link':
            return {key: 'A link has no mode or owner of its own.'};
        case 'invalid-path':
        case 'invalid':
            return {key: 'The system did not take that name or value.'};
        case 'backup-failed':
            return {key: 'The backup could not be written, so the file was not replaced.'};
        case 'no-session':
        case 'no-ticket':
            return {key: 'The session is over.'};
        case 'insecure-transport':
            return {key: 'The shell works over HTTPS only. Open the system with https:// in the address.'};
        case 'tunnel-refused':
            return {key: 'The shell is not offered through a tunnel addon. An administrator can allow it in the settings below.'};
        default:
            return {key: 'The system refused: {message}', values: {message: e instanceof Error ? e.message : String(e)}};
    }
}

/** A folder's entries as the tree shows them: folders (and links to folders) first, then by name. */
export function sorted(entries: Entry[]): Entry[] {
    const isDir = (e: Entry) => e.type === 'dir' || (e.type === 'link' && e.target_type === 'dir');
    return [...entries].sort((a, b) => Number(isDir(b)) - Number(isDir(a)) || (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
}

export function isFolder(e: Pick<Entry, 'type' | 'target_type'>): boolean {
    return e.type === 'dir' || (e.type === 'link' && e.target_type === 'dir');
}

/** Whether the editor opens it: a file, or a link to one. */
export function isOpenable(e: Pick<Entry, 'type' | 'target_type'>): boolean {
    return e.type === 'file' || (e.type === 'link' && e.target_type === 'file');
}
