<script lang="ts">
    // The terminal, in a tab of its own that no page can frame: first the sign-in sheet (root's
    // password, for this one session), then xterm.js under a bar that never goes away - "Shell
    // (root)", who, from where, since when, when it ends without input, and the button that ends
    // it. The session's handle lives in this tab's memory (and its sessionStorage, for a reload
    // within the grace); it is never part of an address.
    import {onDestroy, onMount, tick} from 'svelte';
    import {FitAddon} from '@xterm/addon-fit';
    import {Terminal} from '@xterm/xterm';
    import {Dialog} from 'occulite-ui';
    import {i18n, t} from 'occulite-ui/i18n';
    import {theme} from 'occulite-ui/theme';

    import {ApiError, apiUrl, del, get, post} from './api.js';
    import {clock, countdown, endReason, pasteLines, signInError} from './model.js';
    import type {Session, State} from './model.js';

    const STORE = 'shell.session';

    type Phase = 'loading' | 'signin' | 'connecting' | 'open' | 'ended';
    let phase = $state<Phase>('loading');
    let info = $state<State | undefined>();
    let password = $state('');
    let error = $state('');
    let session = $state<Session | undefined>();
    let ended = $state('');
    let now = $state(Date.now());
    let skew = $state(0);
    let lastInput = $state(0);
    let idleMs = $state(0);
    let host = $state<HTMLDivElement | undefined>();
    let passwordField = $state<HTMLInputElement | undefined>();
    let pasting = $state<{text: string; lines: number} | undefined>();
    let pasteAsked = false;

    let term: Terminal | undefined;
    let fit: FitAddon | undefined;
    let ws: WebSocket | undefined;
    let handle = '';
    let resizeTimer: ReturnType<typeof setTimeout> | undefined;
    let observer: ResizeObserver | undefined;
    const encoder = new TextEncoder();
    let pending: Uint8Array[] = [];

    const LIGHT = {background: '#ffffff', foreground: '#1f2328', cursor: '#1f2328', cursorAccent: '#ffffff', selectionBackground: '#b6d5ff'};
    const DARK = {background: '#0d1117', foreground: '#e6edf3', cursor: '#e6edf3', cursorAccent: '#0d1117', selectionBackground: '#264f78'};

    onMount(() => {
        const tick = setInterval(() => (now = Date.now()), 500);
        void start();
        return () => clearInterval(tick);
    });

    onDestroy(() => {
        observer?.disconnect();
        ws?.close();
        term?.dispose();
    });

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
        handle = '';
        password = '';
        phase = 'signin';
        try {
            info = await get<State>('api/v1/state');
            skew = info.now - Date.now();
        } catch (e) {
            fail(e);
        }
        queueMicrotask(() => passwordField?.focus());
    }

    function fail(e: unknown) {
        const code = e instanceof ApiError ? e.code : '';
        const m = signInError(code, e instanceof ApiError ? (e.body as {tries_left?: number; until?: number}) : undefined, Date.now() + skew, i18n.language);
        error = t(m.key, m.values);
    }

    async function connect(ev: Event) {
        ev.preventDefault();
        if (!password || phase === 'connecting') return;
        error = '';
        phase = 'connecting';
        try {
            const r = await post<{session: Session; handle: string; now: number}>('api/v1/sessions', {password, cols: 80, rows: 24});
            password = '';
            handle = r.handle;
            skew = r.now - Date.now();
            sessionStorage.setItem(STORE, JSON.stringify({id: r.session.id, handle}));
            attach(r.session.id, false);
        } catch (e) {
            password = '';
            phase = 'signin';
            fail(e);
            queueMicrotask(() => passwordField?.focus());
        }
    }

    function attach(id: string, resumed: boolean) {
        const url = new URL(apiUrl(`api/v1/sessions/${encodeURIComponent(id)}/tty`));
        url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
        const sock = new WebSocket(url);
        sock.binaryType = 'arraybuffer';
        ws = sock;
        let ready = false;
        sock.onopen = () => sock.send(JSON.stringify({handle, cols: term?.cols ?? 80, rows: term?.rows ?? 24}));
        sock.onmessage = (ev) => {
            if (typeof ev.data !== 'string') {
                const bytes = new Uint8Array(ev.data as ArrayBuffer);
                // output that arrives before the terminal is drawn waits for it
                if (term) term.write(bytes);
                else pending.push(bytes);
                return;
            }
            const m = JSON.parse(ev.data) as {t: string; session?: Session; now?: number; reason?: string};
            if (m.t === 'ready' && m.session) {
                ready = true;
                session = m.session;
                if (m.now) skew = m.now - Date.now();
                idleMs = m.session.idle_ends - m.session.last_input;
                lastInput = m.session.last_input - skew;
                phase = 'open';
                void tick().then(open);
            } else if (m.t === 'end') {
                finish(m.reason ?? 'lost');
            }
        };
        sock.onclose = (ev) => {
            if (ws !== sock) return;
            if (phase === 'ended') return;
            if (!ready && resumed) {
                // the kept session is gone: the sheet again, without a word
                void signIn();
                return;
            }
            finish(ev.code === 4000 ? ev.reason : ev.code === 4001 ? 'replaced' : 'lost');
        };
    }

    function open() {
        if (!host || term) {
            sendSize();
            return;
        }
        term = new Terminal({
            cursorBlink: true,
            scrollback: 5000,
            fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
            fontSize: 14,
            theme: theme.resolved === 'dark' ? DARK : LIGHT,
            allowProposedApi: false,
        });
        fit = new FitAddon();
        term.loadAddon(fit);
        term.open(host);
        fit.fit();
        for (const bytes of pending) term.write(bytes);
        pending = [];
        term.onData((d) => send(encoder.encode(d)));
        term.onBinary((d) => send(Uint8Array.from(d, (c) => c.charCodeAt(0))));
        // Ctrl+Shift+C copies the selection; Ctrl+C stays the terminal's
        term.attachCustomKeyEventHandler((e) => {
            if (e.type === 'keydown' && e.ctrlKey && e.shiftKey && e.code === 'KeyC') {
                const sel = term?.getSelection();
                if (sel) void navigator.clipboard?.writeText(sel);
                return false;
            }
            return true;
        });
        // a paste of several lines into a root shell is the classic accident: asked once per session
        host.addEventListener(
            'paste',
            (e) => {
                const text = e.clipboardData?.getData('text') ?? '';
                const lines = pasteLines(text);
                if (lines > 1 && !pasteAsked) {
                    e.preventDefault();
                    e.stopPropagation();
                    pasting = {text, lines};
                }
            },
            true,
        );
        observer = new ResizeObserver(() => {
            clearTimeout(resizeTimer);
            resizeTimer = setTimeout(() => {
                fit?.fit();
                sendSize();
            }, 100);
        });
        observer.observe(host);
        sendSize();
        term.focus();
    }

    function send(data: Uint8Array) {
        if (ws?.readyState !== WebSocket.OPEN || phase !== 'open') return;
        lastInput = Date.now();
        ws.send(data);
    }

    function sendSize() {
        if (ws?.readyState === WebSocket.OPEN && term) ws.send(JSON.stringify({t: 'resize', cols: term.cols, rows: term.rows}));
    }

    function confirmPaste() {
        const p = pasting;
        pasting = undefined;
        pasteAsked = true;
        if (p) term?.paste(p.text);
        term?.focus();
    }

    function finish(reason: string) {
        if (phase === 'ended') return;
        ended = reason;
        phase = 'ended';
        sessionStorage.removeItem(STORE);
        if (term) term.options.disableStdin = true;
        ws?.close();
    }

    async function endSession() {
        if (!session) return;
        try {
            await del(`api/v1/sessions/${encodeURIComponent(session.id)}`);
        } catch {
            finish('ended');
        }
    }

    function again() {
        observer?.disconnect();
        observer = undefined;
        term?.dispose();
        term = undefined;
        fit = undefined;
        pending = [];
        pasteAsked = false;
        ended = '';
        void signIn();
    }

    // the server ends the session; the countdown only shows when it will
    const left = $derived(session ? Math.max(0, Math.min(lastInput + idleMs, session.ends - skew) - now) : 0);
    const soon = $derived(phase === 'open' && left <= 60_000);
    $effect(() => {
        document.title = phase === 'open' || phase === 'ended' ? t('Shell (root)') : t('Shell');
    });
</script>

<div class="sh-term-page" lang={i18n.language}>
    {#if phase === 'signin' || phase === 'connecting' || phase === 'loading'}
        <main class="sh-sheet-wrap">
            <form class="sh-sheet" onsubmit={connect} data-testid="signin" autocomplete="off">
                <h1>{t('Shell (root)')}</h1>
                <p>{t('You are opening a root shell on the system. Every session is written to the system\'s log; what you type is not.')}</p>
                <div class="sh-form sh-form-sheet">
                    <span class="sh-label">{t('Signed in to the system as')}</span>
                    <span class="sh-cut sh-strong" title={info?.user ?? ''} data-testid="account">{info?.user ?? ' '}</span>
                    <label for="pw">{t('root\'s password')}</label>
                    <input id="pw" class="hmm-input" type="password" autocomplete="off" bind:value={password} bind:this={passwordField} disabled={phase !== 'signin'} data-testid="password" />
                </div>
                <!-- the error's place is reserved: the sheet keeps its size -->
                <p class="sh-error" role="alert" data-testid="error">{error}</p>
                <div class="sh-actions">
                    <a class="sh-muted sh-small" href="./">{t('To the addon\'s page')}</a>
                    <span class="sh-spacer"></span>
                    <button class="hmm-button sh-wide sh-primary" type="submit" disabled={phase !== 'signin' || !password} data-testid="connect">{phase === 'connecting' ? t('Connecting…') : t('Connect')}</button>
                </div>
                <p class="sh-muted sh-small">{t('The password is asked for every session and is stored nowhere.')}</p>
            </form>
        </main>
    {:else}
        <header class={'sh-bar ' + (phase === 'ended' ? 'ended' : 'live')} data-testid="bar">
            <strong class="sh-bar-title">{t('Shell (root)')}</strong>
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
        <div class="sh-term" bind:this={host} data-testid="terminal"></div>
    {/if}
</div>

<Dialog open={!!pasting} title={t('Paste {n} lines?', {n: String(pasting?.lines ?? 0)})} width="26rem" onclose={() => (pasting = undefined)} testId="paste-dialog">
    <p>{t('Every line runs as root as soon as it arrives. This is asked once per session.')}</p>
    {#snippet buttons()}
        <button class="hmm-button" onclick={() => (pasting = undefined)}>{t('Cancel')}</button>
        <button class="hmm-button sh-danger" onclick={confirmPaste} data-testid="paste-dialog-yes">{t('Paste')}</button>
    {/snippet}
</Dialog>
