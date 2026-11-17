<script lang="ts">
    // Root's files, in a tab of its own that no page can frame: first the sign-in sheet (root's
    // password, for this one session), then a tree of the system's folders beside an editor,
    // under the bar that never goes away. Every file opens read-only; "Edit" is refused, with the
    // reason, where the system is not to be changed (the read-only root file system, the kernel's
    // files, the backups) and for what is no text. The session's handle lives in this tab's
    // memory (and its sessionStorage, for a reload within the grace) and travels in a header,
    // never in an address. Nothing changes its box with its state: buttons, the pill and the
    // status field have one width each, long values are cut and carry the whole text as tooltip.
    import {onDestroy, onMount, tick} from 'svelte';
    import {Dialog, Notices, Tooltip} from 'occulite-ui';
    import {i18n, t} from 'occulite-ui/i18n';
    import {NoticesStore} from 'occulite-ui/notices';
    import {theme} from 'occulite-ui/theme';

    import {ApiError, apiUrl, del, get, post} from './api.js';
    import CodeEditor from './CodeEditor.svelte';
    import {FilesApi, MAX_EDIT, MAX_UPLOAD, fsError, isFolder, isOpenable, reasonText, sorted} from './fs.js';
    import type {Backup, Entry, FileMeta, Id, Info, Listing, Reason} from './fs.js';
    import {clock, countdown, endReason, signInError} from './model.js';
    import type {Session, State} from './model.js';
    import SignIn from './SignIn.svelte';
    import {baseOf, decodeText, dirOf, encodeText, hexDump, joinPath, languageOf, managed, modeBits, modeLetters, sizeText, validMode, validName, validPath, withModeBit} from './text.js';
    import type {Decoded} from './text.js';

    const STORE = 'shell.files';
    const SHORTCUTS = [
        {label: 'Config', path: '/usr/local/etc/config'},
        {label: 'User files', path: '/usr/local'},
        {label: 'Addons', path: '/usr/local/addons'},
        {label: 'System', path: '/'},
        {label: 'USB', path: '/media'},
    ];

    type Phase = 'loading' | 'signin' | 'connecting' | 'open' | 'ended';
    let phase = $state<Phase>('loading');
    let info = $state<State | undefined>();
    let error = $state('');
    let session = $state<Session | undefined>();
    let ended = $state('');
    let now = $state(Date.now());
    let skew = $state(0);
    let lastInput = $state(0);
    let idleMs = $state(0);
    let sheet = $state<SignIn | undefined>();
    const notices = new NoticesStore();

    let ws: WebSocket | undefined;
    let handle = '';
    let api: FilesApi | undefined;

    // a phone gets the plain text field and one pane at a time
    let plain = $state(false);
    let narrow = $state(false);
    let pane = $state<'tree' | 'editor'>('tree');

    // ---- the tree ----
    interface Dir {
        listing?: Listing;
        open: boolean;
        loading: boolean;
        error: string;
    }
    interface Row {
        path: string;
        parent: string;
        entry: Entry;
        depth: number;
        /** why this entry cannot be changed; '' when it can */
        reason: Reason;
    }
    let root = $state('/usr/local');
    let dirs = $state<Record<string, Dir>>({});
    let selected = $state<string>('');
    let goto = $state('/usr/local');
    let busy = $state('');
    let uploading = $state('');

    const rootDir = $derived(dirs[root]);
    const rows = $derived.by(() => {
        const out: Row[] = [];
        const walk = (path: string, depth: number) => {
            const l = dirs[path]?.listing;
            if (!l) return;
            for (const entry of sorted(l.entries)) {
                const p = joinPath(path, entry.name);
                // a link stands in its folder: it can be renamed or deleted where the folder can be changed
                let reason: Reason = entry.type === 'link' ? l.reason : (entry.locked ?? '');
                if (!reason && l.files_read_only) reason = 'setting';
                out.push({path: p, parent: path, entry, depth, reason});
                if (isFolder(entry) && dirs[p]?.open) walk(p, depth + 1);
            }
        };
        walk(root, 0);
        return out;
    });
    const selectedRow = $derived(rows.find((r) => r.path === selected));
    /** where "New" and "Upload" put things: the selected folder, else the selected file's folder, else the tree's top */
    const targetDir = $derived(selectedRow ? (isFolder(selectedRow.entry) ? selectedRow.path : selectedRow.parent) : root);
    const targetReason = $derived.by((): Reason => {
        const l = dirs[targetDir]?.listing;
        if (l) return l.reason || (l.files_read_only ? 'setting' : '');
        return selectedRow && isFolder(selectedRow.entry) ? selectedRow.reason : '';
    });

    // ---- the editor ----
    interface OpenFile {
        path: string;
        state: 'loading' | 'text' | 'binary' | 'too-large' | 'error';
        meta?: FileMeta;
        decoded?: Decoded;
        /** what the editor was built from */
        value: string;
        /** the editor was built from an unsaved buffer (after a comparison) */
        carried: boolean;
        hex: string;
        size: number;
        error: string;
        editing: boolean;
        dirty: boolean;
        /** the SHA-256 of the file as this page last saw it on the system */
        sha: string;
        compare?: {kind: 'conflict' | 'backup'; original: string; sha?: string; backup?: Backup};
    }
    let file = $state<OpenFile | undefined>();
    let editor = $state<CodeEditor | undefined>();
    let status = $state<{text: string; tone: 'idle' | 'ok' | 'bad'}>({text: '', tone: 'idle'});
    const dirty = $derived(!!file && (file.dirty || file.carried));

    // ---- the dialogs ----
    interface Question {
        title: string;
        text: string;
        yes: string;
        danger?: boolean;
        /** the answer counts only when this is typed */
        type?: string;
        resolve: (yes: boolean) => void;
    }
    let question = $state<Question | undefined>();
    let typed = $state('');
    interface Naming {
        kind: 'file' | 'folder' | 'rename';
        title: string;
        label: string;
        value: string;
        error: string;
        path: string;
    }
    let naming = $state<Naming | undefined>();
    interface Props {
        info: Info;
        mode: string;
        uid: number;
        gid: number;
        users: Id[];
        groups: Id[];
        backups: Backup[];
        keep: number;
        root: string;
        error: string;
    }
    let props = $state<Props | undefined>();
    let conflict = $state<{sha256: string; size: number; mtime: number} | undefined>();

    function ask(q: Omit<Question, 'resolve'>): Promise<boolean> {
        typed = '';
        return new Promise((resolve) => (question = {...q, resolve}));
    }

    function answer(yes: boolean) {
        const q = question;
        question = undefined;
        q?.resolve(yes);
    }

    const say = (e: unknown) => {
        const m = fsError(e);
        return t(m.key, m.values);
    };

    onMount(() => {
        const timer = setInterval(() => (now = Date.now()), 500);
        const small = window.matchMedia('(max-width: 760px)');
        const coarse = window.matchMedia('(pointer: coarse) and (max-width: 1024px)');
        // decided once: an editor is not swapped under the user's hands
        plain = small.matches || coarse.matches;
        narrow = small.matches;
        const onSmall = () => (narrow = small.matches);
        small.addEventListener('change', onSmall);
        const leaving = (e: BeforeUnloadEvent) => {
            if (dirty && phase === 'open') e.preventDefault();
        };
        window.addEventListener('beforeunload', leaving);
        void start();
        return () => {
            clearInterval(timer);
            small.removeEventListener('change', onSmall);
            window.removeEventListener('beforeunload', leaving);
        };
    });

    onDestroy(() => ws?.close());

    async function start() {
        // a reload within the grace: the tab still knows its session
        try {
            const kept = JSON.parse(sessionStorage.getItem(STORE) ?? 'null') as {id: string; handle: string} | null;
            if (kept?.id && kept.handle) {
                handle = kept.handle;
                phase = 'connecting';
                attach(kept.id, true);
                return;
            }
        } catch {
            /* no session kept */
        }
        await signIn();
    }

    async function signIn() {
        sessionStorage.removeItem(STORE);
        session = undefined;
        api = undefined;
        handle = '';
        phase = 'signin';
        // emptied now, not after the answer below: the user may be typing by then
        sheet?.reset();
        try {
            info = await get<State>('api/v1/state');
            skew = info.now - Date.now();
        } catch (e) {
            fail(e);
        }
        sheet?.focus();
    }

    function fail(e: unknown) {
        const code = e instanceof ApiError ? e.code : '';
        if (code === 'no-sftp') {
            error = t('The system\'s SSH server offers no file access (SFTP).');
            return;
        }
        const m = signInError(code, e instanceof ApiError ? (e.body as {tries_left?: number; until?: number}) : undefined, Date.now() + skew, i18n.language);
        error = t(m.key, m.values);
    }

    async function connect(password: string) {
        if (phase === 'connecting') return;
        error = '';
        phase = 'connecting';
        try {
            const r = await post<{session: Session; handle: string; now: number}>('api/v1/sessions', {kind: 'files', password});
            handle = r.handle;
            skew = r.now - Date.now();
            sessionStorage.setItem(STORE, JSON.stringify({id: r.session.id, handle}));
            attach(r.session.id, false);
        } catch (e) {
            phase = 'signin';
            fail(e);
            sheet?.reset();
        }
    }

    // the session's socket carries no file: it holds the session for this tab and says at once
    // when the session ended, and why
    function attach(id: string, resumed: boolean) {
        const url = new URL(apiUrl(`api/v1/sessions/${encodeURIComponent(id)}/link`));
        url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
        const sock = new WebSocket(url);
        ws = sock;
        let ready = false;
        sock.onopen = () => sock.send(JSON.stringify({handle}));
        sock.onmessage = (ev) => {
            if (typeof ev.data !== 'string') return;
            const m = JSON.parse(ev.data) as {t: string; session?: Session; now?: number; reason?: string};
            if (m.t === 'ready' && m.session) {
                ready = true;
                session = m.session;
                if (m.now) skew = m.now - Date.now();
                idleMs = m.session.idle_ends - m.session.last_input;
                lastInput = m.session.last_input - skew;
                api = new FilesApi(m.session.id, handle, () => (lastInput = Date.now()));
                phase = 'open';
                void navigate(root);
            } else if (m.t === 'end') {
                finish(m.reason ?? 'lost');
            }
        };
        sock.onclose = (ev) => {
            if (ws !== sock || phase === 'ended') return;
            if (!ready && resumed) {
                void signIn();
                return;
            }
            finish(ev.code === 4000 ? ev.reason : ev.code === 4001 ? 'replaced' : 'lost');
        };
    }

    function finish(reason: string) {
        if (phase === 'ended') return;
        ended = reason;
        phase = 'ended';
        sessionStorage.removeItem(STORE);
        // what is in the editor stays on the screen, to be copied; nothing can be saved any more
        if (file) file.editing = false;
        question = undefined;
        naming = undefined;
        props = undefined;
        conflict = undefined;
        ws?.close();
    }

    async function endSession() {
        if (!session) return;
        if (dirty && !(await ask({title: t('Unsaved changes'), text: t('The changes to {name} are not saved. End the session anyway?', {name: baseOf(file!.path)}), yes: t('End session'), danger: true}))) return;
        try {
            await del(`api/v1/sessions/${encodeURIComponent(session.id)}`);
        } catch {
            finish('ended');
        }
    }

    function again() {
        ended = '';
        file = undefined;
        dirs = {};
        selected = '';
        status = {text: '', tone: 'idle'};
        void signIn();
    }

    // ---- the tree's doings ----

    async function load(path: string): Promise<boolean> {
        if (!api) return false;
        const d = dirs[path] ?? {open: false, loading: false, error: ''};
        dirs[path] = {...d, loading: true, error: ''};
        try {
            const listing = await api.list(path);
            dirs[path] = {listing, open: true, loading: false, error: ''};
            return true;
        } catch (e) {
            dirs[path] = {...d, open: false, loading: false, error: say(e)};
            return false;
        }
    }

    async function navigate(path: string) {
        if (!validPath(path)) return;
        const clean = path.length > 1 ? path.replace(/\/+$/, '') : path;
        root = clean;
        goto = clean;
        selected = '';
        await load(clean);
    }

    async function refresh() {
        // the top and every folder that is open, as they are now
        busy = 'refresh';
        const open = Object.keys(dirs).filter((p) => p === root || dirs[p]?.open);
        await Promise.all(open.map((p) => load(p)));
        busy = '';
    }

    async function clickRow(r: Row) {
        selected = r.path;
        if (isFolder(r.entry)) {
            const d = dirs[r.path];
            if (d?.open) dirs[r.path] = {...d, open: false};
            else await load(r.path);
            return;
        }
        if (isOpenable(r.entry)) await openFile(r.path);
    }

    async function keepChanges(): Promise<boolean> {
        if (!dirty || !file) return false;
        return !(await ask({title: t('Unsaved changes'), text: t('The changes to {name} are not saved. Discard them?', {name: baseOf(file.path)}), yes: t('Discard'), danger: true}));
    }

    // ---- the editor's doings ----

    async function openFile(path: string, quiet = false) {
        if (!api) return;
        if (file && file.path !== path && (await keepChanges())) return;
        file = {path, state: 'loading', value: '', carried: false, hex: '', size: 0, error: '', editing: false, dirty: false, sha: ''};
        if (!quiet) status = {text: '', tone: 'idle'};
        pane = 'editor';
        try {
            const {meta, bytes} = await api.read(path);
            if (file?.path !== path) return;
            if (meta.kind === 'binary') {
                file = {...file, state: 'binary', meta, hex: hexDump(bytes), size: meta.size, sha: meta.sha256};
                return;
            }
            const decoded = decodeText(bytes);
            file = {...file, state: 'text', meta, decoded, value: decoded.text, size: meta.size, sha: meta.sha256};
        } catch (e) {
            if (file?.path !== path) return;
            if (e instanceof ApiError && e.code === 'too-large') {
                file = {...file, state: 'too-large', size: (e.body as {size?: number})?.size ?? 0};
                return;
            }
            file = {...file, state: 'error', error: say(e)};
        }
    }

    async function reload() {
        if (!file) return;
        if (await keepChanges()) return;
        const path = file.path;
        file = undefined;
        await tick();
        await openFile(path);
    }

    function edit() {
        if (!file?.meta?.editable) return;
        file.editing = true;
        void tick().then(() => editor?.focus());
    }

    async function save(replaces?: string) {
        if (!file || !api || !file.decoded || !file.editing || busy === 'save') return;
        const text = editor?.getValue() ?? file.value;
        const bytes = encodeText(text, file.decoded);
        if (bytes.length > MAX_EDIT) {
            status = {text: t('Not saved: {reason}', {reason: t('Files over 2 MB are shown, not edited. Download the file to work on it.')}), tone: 'bad'};
            return;
        }
        busy = 'save';
        try {
            const saved = await api.write(file.path, bytes, replaces ?? file.sha);
            file.sha = saved.sha256 ?? '';
            file.size = saved.size;
            file.carried = false;
            editor?.markClean();
            file.dirty = false;
            const at = clock(Date.now() + skew, Date.now() + skew, i18n.language);
            status = {text: saved.in_place ? t('Saved {time}, written in place', {time: at}) : t('Saved {time}', {time: at}), tone: 'ok'};
            void load(dirOf(file.path));
        } catch (e) {
            if (e instanceof ApiError && e.code === 'conflict') {
                conflict = e.body as {sha256: string; size: number; mtime: number};
                status = {text: t('Not saved: {reason}', {reason: t('The file changed on the system.')}), tone: 'bad'};
            } else {
                // the buffer stays as it is: nothing typed is lost to a failed save
                status = {text: t('Not saved: {reason}', {reason: say(e)}), tone: 'bad'};
            }
        } finally {
            busy = '';
        }
    }

    async function overwrite() {
        const c = conflict;
        conflict = undefined;
        if (c) await save(c.sha256);
    }

    async function reloadAfterConflict() {
        conflict = undefined;
        if (!file) return;
        const path = file.path;
        file = undefined;
        await tick();
        await openFile(path);
    }

    /** The comparison: the version on the system (or a backup) on the left, the editor's text on the right. */
    async function compareWith(kind: 'conflict' | 'backup', backup?: Backup) {
        if (!file || !api || file.state !== 'text') return;
        const source = kind === 'backup' && backup ? backup.path : file.path;
        try {
            const {meta, bytes} = await api.read(source);
            if (meta.kind !== 'text') {
                notices.push('error', t(reasonText('binary')));
                return;
            }
            const buffer = editor?.getValue() ?? file.value;
            file.carried = dirty;
            file.value = buffer;
            file.compare = {kind, original: decodeText(bytes).text, sha: meta.sha256, backup};
            conflict = undefined;
            props = undefined;
        } catch (e) {
            notices.push('error', say(e));
        }
    }

    function endCompare() {
        if (file) file.compare = undefined;
    }

    async function overwriteFromCompare() {
        const sha = file?.compare?.sha;
        endCompare();
        await tick();
        await save(sha);
    }

    async function restore(path: string, b: Backup) {
        if (!api) return;
        if (!(await ask({title: t('Restore this version?'), text: t('{name} is replaced by its version of {time}. The present version goes to the backups first.', {name: baseOf(path), time: clock(b.at, Date.now() + skew, i18n.language)}), yes: t('Restore')}))) return;
        try {
            await api.restore(path, b.name);
            notices.push('info', t('Restored.'));
            props = undefined;
            if (file) file.compare = undefined;
            void load(dirOf(path));
            if (file?.path === path) {
                file = undefined;
                await tick();
                await openFile(path);
            }
        } catch (e) {
            notices.push('error', say(e));
        }
    }

    async function download(path: string) {
        if (!api) return;
        try {
            const d = await api.downloadUrl(path);
            const a = document.createElement('a');
            a.href = d.url;
            a.download = d.name;
            a.rel = 'noopener';
            document.body.append(a);
            a.click();
            a.remove();
        } catch (e) {
            notices.push('error', say(e));
        }
    }

    // ---- new, rename, delete, upload ----

    function askName(kind: Naming['kind']) {
        if (kind === 'rename') {
            if (!selectedRow) return;
            naming = {kind, title: t('Rename'), label: t('New path'), value: selectedRow.path, error: '', path: selectedRow.path};
            return;
        }
        naming = {kind, title: kind === 'file' ? t('New file') : t('New folder'), label: t('Name'), value: '', error: '', path: targetDir};
    }

    async function confirmName() {
        const n = naming;
        if (!n || !api) return;
        const value = n.value.trim();
        if (n.kind === 'rename' ? !validPath(value) || value === n.path : !validName(value)) {
            n.error = t('The system did not take that name or value.');
            return;
        }
        try {
            if (n.kind === 'rename') {
                await api.rename(n.path, value);
                if (file?.path === n.path && !dirty) file = undefined;
                selected = value;
                await Promise.all([load(dirOf(n.path)), dirOf(value) in dirs ? load(dirOf(value)) : Promise.resolve(false)]);
            } else {
                const path = joinPath(n.path, value);
                if (n.kind === 'file') await api.create(path);
                else await api.mkdir(path);
                await load(n.path);
                selected = path;
                if (n.kind === 'file') {
                    await openFile(path);
                    edit();
                }
            }
            naming = undefined;
        } catch (e) {
            n.error = say(e);
        }
    }

    async function remove() {
        const r = selectedRow;
        if (!r || !api) return;
        const name = r.entry.name;
        const text = r.entry.type === 'link' ? t('The link {name} is deleted, not what it leads to.', {name}) : t('{name} is deleted. There is no way back.', {name});
        if (!(await ask({title: t('Delete?'), text, yes: t('Delete'), danger: true}))) return;
        try {
            try {
                await api.remove(r.path, false);
            } catch (e) {
                if (!(e instanceof ApiError) || e.code !== 'not-empty') throw e;
                const n = String((e.body as {entries?: number})?.entries ?? '?');
                const sure = await ask({
                    title: t('Delete the folder with everything in it?'),
                    text: t('{name} holds {n} entries, and what is in them. Type the folder\'s name to delete it all. There is no way back.', {name, n}),
                    yes: t('Delete'),
                    danger: true,
                    type: name,
                });
                if (!sure) return;
                await api.remove(r.path, true);
            }
            if (file && (file.path === r.path || file.path.startsWith(r.path + '/'))) file = undefined;
            selected = '';
            delete dirs[r.path];
            await load(r.parent);
            notices.push('info', t('Deleted.'));
        } catch (e) {
            notices.push('error', say(e));
        }
    }

    let picker = $state<HTMLInputElement | undefined>();
    let dropping = $state(false);

    async function upload(list: FileList | File[] | null | undefined) {
        if (!api || !list) return;
        const dir = targetDir;
        const all = Array.from(list);
        let done = 0;
        for (const f of all) {
            uploading = t('Uploading {name} ({i} of {n})', {name: f.name, i: String(done + 1), n: String(all.length)});
            if (f.size > MAX_UPLOAD) {
                notices.push('error', t('{name} is over 64 MB and was not uploaded.', {name: f.name}));
                continue;
            }
            const path = joinPath(dir, f.name);
            try {
                try {
                    await api.upload(path, f, false);
                } catch (e) {
                    if (!(e instanceof ApiError) || e.code !== 'exists') throw e;
                    const yes = await ask({title: t('Replace the file?'), text: t('{name} exists already. Its present version goes to the backups first (up to 50 MB).', {name: f.name}), yes: t('Replace'), danger: true});
                    if (!yes) continue;
                    const saved = await api.upload(path, f, true);
                    if (saved.backup_skipped) notices.push('warn', t('{name} was replaced without a backup: it was too large for one.', {name: f.name}));
                }
                done++;
            } catch (e) {
                notices.push('error', f.name + ': ' + say(e));
            }
        }
        uploading = '';
        if (picker) picker.value = '';
        if (done > 0) notices.push('info', t('{n} files uploaded.', {n: String(done)}));
        await load(dir);
        if (file && all.some((f) => joinPath(dir, f.name) === file!.path) && !dirty) await openFile(file.path);
    }

    function drop(e: DragEvent) {
        e.preventDefault();
        dropping = false;
        if (targetReason) {
            notices.push('error', t(reasonText(targetReason)));
            return;
        }
        void upload(e.dataTransfer?.files);
    }

    // ---- properties ----

    async function openProps() {
        const r = selectedRow;
        if (!r || !api) return;
        try {
            const [st, ids, b] = await Promise.all([api.stat(r.path), api.ids(), isOpenable(r.entry) ? api.backups(r.path) : Promise.resolve({backups: [] as Backup[], keep: 0, keep_bytes: 0, root: ''})]);
            const users = ids.users.some((u) => u.id === st.uid) ? ids.users : [...ids.users, {id: st.uid, name: String(st.uid)}];
            const groups = ids.groups.some((g) => g.id === st.gid) ? ids.groups : [...ids.groups, {id: st.gid, name: String(st.gid)}];
            props = {info: st, mode: st.mode, uid: st.uid, gid: st.gid, users, groups, backups: b.backups, keep: b.keep, root: b.root, error: ''};
        } catch (e) {
            notices.push('error', say(e));
        }
    }

    const propsChanged = $derived(!!props && (props.mode !== props.info.mode || props.uid !== props.info.uid || props.gid !== props.info.gid));
    const propsReason = $derived.by(() => {
        if (!props) return '';
        if (props.info.type === 'link') return 'A link has no mode or owner of its own.';
        const reason = props.info.reason || (selectedRow?.reason === 'setting' ? 'setting' : '');
        return reasonText(reason);
    });

    async function applyProps() {
        const p = props;
        if (!p || !api) return;
        if (!validMode(p.mode)) {
            p.error = t('A mode is one to four digits from 0 to 7.');
            return;
        }
        try {
            // the owner first: a change of owner clears the set-id bits
            if (p.uid !== p.info.uid || p.gid !== p.info.gid) await api.chown(p.info.path, p.uid, p.gid);
            if (p.mode.padStart(4, '0') !== p.info.mode) await api.chmod(p.info.path, p.mode);
            props = undefined;
            notices.push('info', t('Saved.'));
            await load(dirOf(p.info.path));
        } catch (e) {
            p.error = say(e);
        }
    }

    // the server ends the session; the countdown only shows when it will
    const left = $derived(session ? Math.max(0, Math.min(lastInput + idleMs, session.ends - skew) - now) : 0);
    const soon = $derived(phase === 'open' && left <= 60_000);
    const live = $derived(phase === 'open');
    $effect(() => {
        document.title = phase === 'open' || phase === 'ended' ? t('Shell (root)') : t('Shell');
    });

    const filePill = $derived.by((): {label: string; tone: string} => {
        if (!file || file.state === 'loading' || file.state === 'error') return {label: '', tone: 'idle'};
        if (file.compare) return {label: 'Comparing', tone: 'idle'};
        if (file.state === 'binary') return {label: 'Binary', tone: 'idle'};
        if (file.state === 'too-large') return {label: 'Too large', tone: 'idle'};
        return file.editing ? {label: 'Editing', tone: 'warn'} : {label: 'Read-only', tone: 'idle'};
    });
    const editBlocked = $derived.by(() => {
        if (!live) return 'The session is over.';
        if (!file || file.state !== 'text' || !file.meta) return '';
        if (file.meta.editable) return '';
        return reasonText(file.meta.why);
    });
    const note = $derived.by(() => {
        if (!file) return '';
        if (file.compare?.kind === 'conflict') return t('Left: the file as it is on the system now. Right: your text.');
        if (file.compare?.backup) return t('Left: the backup of {time}. Right: the file as it is in the editor.', {time: when(file.compare.backup.at)});
        if (file.state === 'binary') return t(reasonText('binary'));
        if (file.state !== 'text' || !file.meta) return '';
        const parts: string[] = [];
        if (file.meta.real !== file.path) parts.push(t('Opened through a link: the file is {real}.', {real: file.meta.real}));
        if (managed(file.meta.real)) parts.push(t('Managed by the system: it may overwrite or refuse a change here.'));
        if (file.meta.truncated) parts.push(t('Only the first part is shown.'));
        if (file.decoded?.mixed) parts.push(t('This file has both kinds of line ends. A save writes one kind throughout.'));
        if (!file.meta.editable && file.meta.why) parts.push(t(reasonText(file.meta.why)));
        return parts.join(' ');
    });
    const selReason = $derived(selectedRow ? reasonText(selectedRow.reason) : '');
    // inside the backups a delete is the one change there is
    const delReason = $derived(selectedRow?.reason === 'backups' && selectedRow.path !== '/usr/local/shell-backups' ? '' : selReason);
    const when = (ms: number) => clock(ms, now + skew, i18n.language);
</script>

{#snippet icon(kind: string)}
    <svg class="sh-ico" viewBox="0 0 16 16" aria-hidden="true">
        {#if kind === 'dir'}
            <path d="M1.5 3.5h4.2l1.4 1.6h7.4v8.4h-13z" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round" />
        {:else if kind === 'link'}
            <path d="M6.5 9.5l3-3M5 8L3.6 9.4a2.1 2.1 0 003 3L8 11M8 5l1.4-1.4a2.1 2.1 0 013 3L11 8" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" />
        {:else if kind === 'lock'}
            <path d="M4 7.5h8v6H4zM5.6 7.5V5.4a2.4 2.4 0 014.8 0v2.1" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round" />
        {:else if kind === 'other'}
            <circle cx="8" cy="8" r="4.5" fill="none" stroke="currentColor" stroke-width="1.2" />
        {:else}
            <path d="M3.5 1.5h6l3 3v10h-9zM9.5 1.5v3h3" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round" />
        {/if}
    </svg>
{/snippet}

{#snippet act(label: string, testId: string, onclick: () => void, blocked: string, extra = '')}
    <Tooltip text={blocked ? t(blocked) : ''} testId={testId + '-tip'}>
        <button class={'hmm-button sh-act ' + extra} {onclick} disabled={!!blocked || !live} data-testid={testId}>{t(label)}</button>
    </Tooltip>
{/snippet}

<div class="sh-term-page" lang={i18n.language}>
    {#if phase === 'signin' || phase === 'connecting' || phase === 'loading'}
        <SignIn bind:this={sheet} kind="files" user={info?.user ?? ''} busy={phase === 'connecting'} ready={phase !== 'loading'} {error} onconnect={connect} />
    {:else}
        <header class={'sh-bar ' + (phase === 'ended' ? 'ended' : 'live')} data-testid="bar">
            <strong class="sh-bar-title">{t('Shell (root)')}</strong>
            <span class="sh-bar-kind" data-testid="bar-kind">{t('Files')}</span>
            {#if session}
                <span class="sh-cut sh-bar-item" title={session.user} data-testid="bar-user">{session.user}</span>
                <span class="sh-cut sh-bar-item sh-mono" title={session.client}>{t('from {client}', {client: session.client})}</span>
                <span class="sh-bar-item sh-num">{t('since {time}', {time: clock(session.since, now + skew, i18n.language)})}</span>
            {/if}
            <span class="sh-spacer"></span>
            <!-- one box for both states: the countdown while it runs, the reason when it is over -->
            <span class={'sh-bar-state' + (soon ? ' soon' : '')} data-testid="bar-state" title={phase === 'ended' ? t(endReason(ended)) : ''}>
                {#if phase === 'ended'}
                    {t('Session ended')}: {t(endReason(ended))}
                {:else}
                    {t('ends without input in')} <span class="sh-num" data-testid="countdown">{countdown(left)}</span>
                {/if}
            </span>
            {#if phase === 'ended'}
                <button class="hmm-button sh-wide" onclick={again} data-testid="again">{t('New session')}</button>
            {:else}
                <button class="hmm-button sh-wide" onclick={endSession} data-testid="end-session">{t('End session')}</button>
            {/if}
        </header>

        <div class={'sh-files' + (narrow ? ' narrow show-' + pane : '')} data-testid="files">
            <!-- the tree -->
            <section class="sh-tree-pane" data-testid="tree-pane" aria-label={t('Folders')}>
                <nav class="sh-shortcuts" aria-label={t('Places')}>
                    {#each SHORTCUTS as s (s.path)}
                        <button class={'hmm-button sh-short' + (root === s.path ? ' on' : '')} onclick={() => navigate(s.path)} disabled={!live} title={s.path} data-testid={'place-' + s.path}>{t(s.label)}</button>
                    {/each}
                </nav>
                <form class="sh-goto" onsubmit={(e) => (e.preventDefault(), void navigate(goto.trim()))}>
                    <input class="hmm-input sh-mono" bind:value={goto} disabled={!live} aria-label={t('Folder')} spellcheck="false" autocapitalize="off" data-testid="goto" />
                    <button class="hmm-button sh-go" type="submit" disabled={!live || !validPath(goto.trim())} data-testid="go">{t('Go')}</button>
                </form>
                <div class="sh-acts">
                    {@render act('New file', 'new-file', () => askName('file'), reasonText(targetReason))}
                    {@render act('New folder', 'new-folder', () => askName('folder'), reasonText(targetReason))}
                    {@render act('Upload', 'upload', () => picker?.click(), reasonText(targetReason))}
                    <button class="hmm-button sh-act" onclick={refresh} disabled={!live || busy === 'refresh'} data-testid="refresh">{t('Refresh')}</button>
                </div>
                <input class="sh-hidden" type="file" multiple bind:this={picker} onchange={() => upload(picker?.files)} data-testid="picker" tabindex="-1" aria-hidden="true" />
                <!-- where "New" and "Upload" go, and why not: one line, always there -->
                <p class="sh-where" data-testid="where" title={targetDir}>
                    {#if uploading}
                        {uploading}
                    {:else}
                        <span class="sh-muted">{t('In')}</span> <span class="sh-mono">{targetDir}</span>
                    {/if}
                </p>
                <div
                    class={'sh-tree' + (dropping ? ' dropping' : '')}
                    role="tree"
                    tabindex="-1"
                    aria-label={root}
                    data-testid="tree"
                    ondragover={(e) => {
                        e.preventDefault();
                        dropping = true;
                    }}
                    ondragleave={() => (dropping = false)}
                    ondrop={drop}
                >
                    {#if rootDir?.error}
                        <p class="sh-tree-note sh-error-text" role="alert" data-testid="tree-error">{rootDir.error}</p>
                    {:else if !rootDir?.listing}
                        <p class="sh-tree-note sh-muted">{t('Loading…')}</p>
                    {:else}
                        {#if rootDir.listing.real !== root}
                            <p class="sh-tree-note sh-muted sh-small" data-testid="tree-real">{t('{path} is a link to {real}.', {path: root, real: rootDir.listing.real})}</p>
                        {/if}
                        {#if rootDir.listing.reason || rootDir.listing.files_read_only}
                            <p class="sh-tree-note sh-small" data-testid="tree-reason">{@render icon('lock')} {t(reasonText(rootDir.listing.reason || 'setting'))}</p>
                        {/if}
                        {#each rows as r (r.path)}
                            {@const folder = isFolder(r.entry)}
                            {@const d = dirs[r.path]}
                            <!-- the lock says what is in the folder cannot be changed: known from its listing once it
                                 is open; a link's target is not known before -->
                            {@const lock = folder ? (d?.listing ? d.listing.reason : r.entry.type === 'link' ? '' : r.reason) : ''}
                            <button
                                class={'sh-row' + (selected === r.path ? ' on' : '')}
                                style={'padding-left:' + (6 + r.depth * 14) + 'px'}
                                role="treeitem"
                                aria-selected={selected === r.path}
                                aria-expanded={folder ? !!d?.open : undefined}
                                onclick={() => clickRow(r)}
                                disabled={!live}
                                data-testid="row"
                                data-path={r.path}
                                title={r.entry.type === 'link' ? r.path + ' → ' + (r.entry.target ?? '') : r.path}
                            >
                                <span class="sh-chev">{folder ? (d?.open ? '▾' : '▸') : ''}</span>
                                {@render icon(r.entry.type === 'link' ? 'link' : folder ? 'dir' : r.entry.type === 'file' ? 'file' : 'other')}
                                <span class={'sh-name' + (r.entry.type === 'link' && !r.entry.target_type ? ' broken' : '')}>{r.entry.name}{#if r.entry.type === 'link'}<span class="sh-target"> → {r.entry.target}</span>{/if}</span>
                                {#if lock === 'read-only' || lock === 'kernel'}<span class="sh-lock" title={t(reasonText(lock))} data-testid="lock">{@render icon('lock')}</span>{/if}
                                <span class="sh-size sh-num">{folder ? '' : sizeText(r.entry.size)}</span>
                                <span class="sh-time sh-num">{when(r.entry.mtime)}</span>
                            </button>
                            {#if d?.error}
                                <p class="sh-tree-note sh-error-text sh-small" style={'padding-left:' + (26 + r.depth * 14) + 'px'}>{d.error}</p>
                            {:else if folder && d?.open && d.listing && d.listing.entries.length === 0}
                                <p class="sh-tree-note sh-muted sh-small" style={'padding-left:' + (40 + r.depth * 14) + 'px'}>{t('Empty.')}</p>
                            {/if}
                        {:else}
                            <p class="sh-tree-note sh-muted">{t('Empty.')}</p>
                        {/each}
                        {#if rootDir.listing.truncated}
                            <p class="sh-tree-note sh-muted sh-small">{t('This folder holds more than is shown here.')}</p>
                        {/if}
                    {/if}
                </div>
                <!-- the selection: one strip, always there, the same height with and without one -->
                <div class="sh-sel" data-testid="selection">
                    <p class="sh-sel-line sh-cut" title={selectedRow?.path ?? ''}>
                        {#if selectedRow}
                            <span class="sh-strong">{selectedRow.entry.name}</span>
                        {:else}
                            <span class="sh-muted">{t('Nothing selected.')}</span>
                        {/if}
                    </p>
                    <p class="sh-sel-line sh-cut sh-small sh-muted sh-num" data-testid="selection-detail">
                        {#if selectedRow}
                            {selectedRow.entry.mode} {modeLetters(selectedRow.entry.mode)} · {selectedRow.entry.owner}:{selectedRow.entry.group}{#if !isFolder(selectedRow.entry)} · {sizeText(selectedRow.entry.size)}{/if} · {when(selectedRow.entry.mtime)}
                        {/if}
                    </p>
                    <div class="sh-acts">
                        {@render act('Download', 'download', () => selectedRow && download(selectedRow.path), !selectedRow ? 'Nothing selected.' : isOpenable(selectedRow.entry) ? '' : 'Only a file is downloaded.')}
                        {@render act('Rename', 'rename', () => askName('rename'), !selectedRow ? 'Nothing selected.' : selReason)}
                        {@render act('Delete', 'delete', remove, !selectedRow ? 'Nothing selected.' : delReason)}
                        {@render act('Properties', 'properties', openProps, !selectedRow ? 'Nothing selected.' : '')}
                    </div>
                </div>
            </section>

            <!-- the editor -->
            <section class="sh-edit-pane" data-testid="editor-pane" aria-label={t('File')}>
                <div class="sh-edit-head">
                    {#if narrow}
                        <button class="hmm-button sh-back" onclick={() => (pane = 'tree')} data-testid="back">{t('Folders')}</button>
                    {/if}
                    <span class="sh-cut sh-mono sh-edit-path" title={file?.path ?? ''} data-testid="file-path">{file?.path ?? ''}</span>
                    <span class={'sh-pill sh-pill-mode ' + filePill.tone} data-testid="file-pill" title={filePill.label ? t(filePill.label) : ''}>{filePill.label ? t(filePill.label) : '\u00a0'}</span>
                </div>
                <div class="sh-edit-tools">
                    <!-- the status keeps its box: what the last save did, or why it did not -->
                    <span class={'sh-status sh-cut ' + status.tone} title={status.text} role="status" data-testid="status">{status.text || '\u00a0'}</span>
                    <span class="sh-spacer"></span>
                    {#if file?.compare}
                        {#if file.compare.kind === 'conflict'}
                            {@render act('Overwrite', 'compare-overwrite', overwriteFromCompare, '', 'sh-danger')}
                        {:else}
                            {@render act('Restore', 'compare-restore', () => file?.compare?.backup && restore(file.path, file.compare.backup), editBlocked)}
                        {/if}
                        <button class="hmm-button sh-act" onclick={endCompare} data-testid="compare-back">{t('Back')}</button>
                    {:else}
                        {#if file?.editing}
                            <button class="hmm-button sh-act sh-primary" onclick={() => save()} disabled={!dirty || busy === 'save' || !live} data-testid="save">{t('Save')}</button>
                        {:else}
                            {@render act('Edit', 'edit', edit, file?.state === 'text' ? editBlocked : 'Nothing to edit.')}
                        {/if}
                        <button class="hmm-button sh-act" onclick={reload} disabled={!file || file.state === 'loading' || !live} data-testid="reload">{t('Reload')}</button>
                    {/if}
                </div>
                <!-- what the user should know about this file: one line, always there -->
                <p class="sh-edit-note sh-small sh-cut" data-testid="file-note" title={note}>{note || '\u00a0'}</p>
                <div class="sh-edit-body" data-testid="editor-body">
                    {#if !file}
                        <p class="sh-edit-empty sh-muted">{t('Choose a file on the left. It opens read-only.')}</p>
                    {:else if file.state === 'loading'}
                        <p class="sh-edit-empty sh-muted">{t('Loading…')}</p>
                    {:else if file.state === 'error'}
                        <p class="sh-edit-empty sh-error-text" role="alert" data-testid="file-error">{file.error}</p>
                    {:else if file.state === 'too-large'}
                        <div class="sh-edit-empty">
                            <p>{t('{name} has {size}. Files over 10 MB are not opened here. Logs are better read on the system\'s Log page.', {name: baseOf(file.path), size: sizeText(file.size)})}</p>
                            <button class="hmm-button sh-act" onclick={() => download(file!.path)} disabled={!live} data-testid="download-open">{t('Download')}</button>
                        </div>
                    {:else if file.state === 'binary'}
                        <pre class="sh-hex" data-testid="hex">{file.hex}</pre>
                    {:else}
                        {#key file.path}
                            <CodeEditor
                                bind:this={editor}
                                value={file.value}
                                language={languageOf(baseOf(file.path), file.value.slice(0, 200).split('\n')[0] ?? '')}
                                readOnly={!file.editing || !live}
                                dark={theme.resolved === 'dark'}
                                {plain}
                                original={file.compare?.original}
                                large={file.size > MAX_EDIT}
                                ondirty={(d) => file && (file.dirty = d)}
                                onsave={() => save()}
                            />
                        {/key}
                    {/if}
                </div>
            </section>
        </div>
    {/if}
    <Notices notices={notices.visible} ondismiss={(id) => notices.dismiss(id)} />
</div>

<Dialog open={!!question} title={question?.title ?? ''} width="30rem" onclose={() => answer(false)} testId="question">
    <p>{question?.text}</p>
    {#if question?.type}
        <input class="hmm-input sh-full sh-mono" bind:value={typed} placeholder={question.type} aria-label={t('The folder\'s name')} spellcheck="false" autocapitalize="off" data-testid="question-type" />
    {/if}
    {#snippet buttons()}
        <button class="hmm-button" onclick={() => answer(false)} data-testid="question-no">{t('Cancel')}</button>
        <button class={'hmm-button ' + (question?.danger ? 'sh-danger' : 'sh-primary')} onclick={() => answer(true)} disabled={!!question?.type && typed !== question.type} data-testid="question-yes">{question?.yes}</button>
    {/snippet}
</Dialog>

<Dialog open={!!naming} title={naming?.title ?? ''} width="32rem" onclose={() => (naming = undefined)} testId="naming">
    {#if naming}
        <form onsubmit={(e) => (e.preventDefault(), void confirmName())}>
            <p class="sh-muted sh-small sh-cut" title={naming.path}>{naming.kind === 'rename' ? t('A path in another folder moves it there. Nothing is overwritten.') : t('In {folder}', {folder: naming.path})}</p>
            <label class="sh-stack">
                <span>{naming.label}</span>
                <input class="hmm-input sh-full sh-mono" bind:value={naming.value} spellcheck="false" autocapitalize="off" data-testid="naming-value" />
            </label>
            <p class="sh-error" role="alert" data-testid="naming-error">{naming.error}</p>
        </form>
    {/if}
    {#snippet buttons()}
        <button class="hmm-button" onclick={() => (naming = undefined)}>{t('Cancel')}</button>
        <button class="hmm-button sh-primary" onclick={confirmName} disabled={!naming?.value.trim()} data-testid="naming-yes">{naming?.kind === 'rename' ? t('Rename') : t('Create')}</button>
    {/snippet}
</Dialog>

<Dialog open={!!conflict} title={t('The file changed on the system')} width="34rem" onclose={() => (conflict = undefined)} testId="conflict">
    {#if conflict && file}
        <p>{t('{name} is not what it was when you opened it: someone or something wrote it at {time}. Your text is still in the editor.', {name: baseOf(file.path), time: when(conflict.mtime)})}</p>
    {/if}
    {#snippet buttons()}
        <button class="hmm-button" onclick={reloadAfterConflict} data-testid="conflict-reload">{t('Reload')}</button>
        <Tooltip text={plain ? t('The comparison needs the large editor, which a small screen does not load.') : ''}>
            <button class="hmm-button" onclick={() => compareWith('conflict')} disabled={plain} data-testid="conflict-compare">{t('Compare')}</button>
        </Tooltip>
        <button class="hmm-button sh-danger" onclick={overwrite} data-testid="conflict-overwrite">{t('Overwrite')}</button>
    {/snippet}
</Dialog>

<Dialog open={!!props} title={t('Properties')} width="36rem" onclose={() => (props = undefined)} testId="props">
    {#if props}
        {@const bits = modeBits(validMode(props.mode) ? props.mode : '0')}
        <dl class="sh-rows sh-props">
            <dt>{t('Path')}</dt>
            <dd><span class="sh-cut sh-mono" title={props.info.path}>{props.info.path}</span></dd>
            {#if props.info.type === 'link'}
                <dt>{t('Link to')}</dt>
                <dd><span class="sh-cut sh-mono" title={props.info.target}>{props.info.target}{props.info.target_type ? '' : ' (' + t('broken') + ')'}</span></dd>
            {/if}
            <dt>{t('Size')}</dt>
            <dd class="sh-num">{sizeText(props.info.size)}</dd>
            <dt>{t('Changed')}</dt>
            <dd class="sh-num">{when(props.info.mtime)}</dd>
        </dl>
        <fieldset class="sh-fieldset" disabled={!!propsReason || !live}>
            <legend>{t('Mode and owner')}</legend>
            <div class="sh-form sh-form-props">
                <label for="p-mode">{t('Mode (octal)')}</label>
                <input id="p-mode" class="hmm-input sh-number sh-mono" bind:value={props.mode} maxlength="4" inputmode="numeric" data-testid="props-mode" />
                <span class="sh-mono sh-num" data-testid="props-letters">{validMode(props.mode) ? modeLetters(props.mode) : ''}</span>
            </div>
            <table class="sh-bits">
                <thead>
                    <tr><th></th><th>{t('read')}</th><th>{t('write')}</th><th>{t('execute')}</th></tr>
                </thead>
                <tbody>
                    {#each ['Owner', 'Group', 'Others'] as who, w (who)}
                        <tr>
                            <th>{t(who)}</th>
                            {#each [0, 1, 2] as b (b)}
                                <td><input type="checkbox" checked={bits[w * 3 + b]} onchange={(e) => props && (props.mode = withModeBit(validMode(props.mode) ? props.mode : '0', w * 3 + b, e.currentTarget.checked))} aria-label={t(who) + ' ' + t(['read', 'write', 'execute'][b]!)} data-testid={'bit-' + (w * 3 + b)} /></td>
                            {/each}
                        </tr>
                    {/each}
                </tbody>
            </table>
            <div class="sh-form sh-form-props">
                <label for="p-owner">{t('Owner')}</label>
                <select id="p-owner" class="hmm-select" bind:value={props.uid} data-testid="props-owner">
                    {#each props.users as u (u.id)}<option value={u.id}>{u.name} ({u.id})</option>{/each}
                </select>
                <span></span>
                <label for="p-group">{t('Group')}</label>
                <select id="p-group" class="hmm-select" bind:value={props.gid} data-testid="props-group">
                    {#each props.groups as g (g.id)}<option value={g.id}>{g.name} ({g.id})</option>{/each}
                </select>
                <span></span>
            </div>
            <p class="sh-muted sh-small">{t('Applied to this one entry, never to what is in a folder.')}</p>
        </fieldset>
        <!-- why it cannot be changed, or what went wrong: its place is reserved -->
        <p class={'sh-error' + (props.error ? '' : ' sh-muted')} role="alert" data-testid="props-error">{props.error || (propsReason ? t(propsReason) : '')}</p>
        {#if isOpenable(props.info)}
            <h3 class="sh-h3">{t('Backups')}</h3>
            <p class="sh-muted sh-small">{t('Every save keeps the version it replaces: the last {n} of each file, 50 MB in all, in {root}. They stay when the addon is uninstalled.', {n: String(props.keep), root: props.root})}</p>
            <div class="sh-backups" data-testid="backups">
                {#each props.backups as b (b.name)}
                    <div class="sh-backup" data-testid="backup">
                        <span class="sh-num">{when(b.at)}</span>
                        <span class="sh-num sh-muted">{sizeText(b.size)}</span>
                        <span class="sh-spacer"></span>
                        <Tooltip text={plain ? t('The comparison needs the large editor, which a small screen does not load.') : file?.path === props.info.path && file.state === 'text' ? '' : t('Open the file first.')}>
                            <button class="hmm-button sh-narrow" onclick={() => compareWith('backup', b)} disabled={plain || !live || file?.path !== props.info.path || file.state !== 'text'} data-testid="backup-compare">{t('Compare')}</button>
                        </Tooltip>
                        <Tooltip text={propsReason ? t(propsReason) : ''}>
                            <button class="hmm-button sh-narrow" onclick={() => props && restore(props.info.path, b)} disabled={!!propsReason || !live} data-testid="backup-restore">{t('Restore')}</button>
                        </Tooltip>
                    </div>
                {:else}
                    <div class="sh-muted" data-testid="no-backups">{t('No backup yet.')}</div>
                {/each}
            </div>
        {/if}
    {/if}
    {#snippet buttons()}
        <button class="hmm-button" onclick={() => (props = undefined)}>{t('Close')}</button>
        <button class="hmm-button sh-primary" onclick={applyProps} disabled={!propsChanged || !!propsReason || !live} data-testid="props-apply">{t('Apply')}</button>
    {/snippet}
</Dialog>
