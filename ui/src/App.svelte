<script lang="ts">
    // The addon's page, framed by the system's shell: what state the root shell is in (the
    // addon's switch, SSH, the connection), the switch with its time limit, the button that opens
    // the terminal in a tab of its own, the open sessions, the limits, the audit lines. Nothing
    // here changes its box with its state: the pills have one width, the buttons keep theirs,
    // long values are cut and carry the whole text as their tooltip.
    import {onMount} from 'svelte';
    import {Dialog, Notices, ThemeSwitch, Tooltip} from 'occulite-ui';
    import {i18n, t} from 'occulite-ui/i18n';
    import {NoticesStore} from 'occulite-ui/notices';
    import {cycleTheme, theme} from 'occulite-ui/theme';

    import {apiUrl, del, get, put} from './api.js';
    import {addonPill, blockReason, clock, countdown, settingsEqual, settingsValid, sshPill, transportPill, windowLabel} from './model.js';
    import type {AuditEntry, Pill, Session, Settings, State} from './model.js';

    const notices = new NoticesStore();
    let state_ = $state<State | undefined>();
    let audit = $state<AuditEntry[]>([]);
    let loadError = $state('');
    let now = $state(Date.now());
    // the server's clock: its "now" plus the time since the answer
    let skew = $state(0);
    let form = $state<Settings | undefined>();
    let busy = $state('');
    let enabling = $state(false);
    let hours = $state(8);

    async function load() {
        try {
            const s = await get<State>('api/v1/state');
            skew = s.now - Date.now();
            state_ = s;
            if (!form || settingsEqual(form, lastStored)) form = {...s.settings};
            lastStored = {...s.settings};
            loadError = '';
        } catch (e) {
            loadError = (e as Error).message;
        }
        try {
            audit = await get<AuditEntry[]>('api/v1/audit');
        } catch {
            /* the state's error says it */
        }
    }
    let lastStored: Settings = {idle_minutes: 0, max_hours: 0, max_sessions: 0, allow_tunnel: false};

    onMount(() => {
        void load();
        // one stream instead of a poll: a poll would count as the user's activity on the system
        // and keep the web session from ever idling out while this page is open
        let es: EventSource | undefined;
        let retry: ReturnType<typeof setTimeout> | undefined;
        const connect = () => {
            es = new EventSource(apiUrl('api/v1/events'));
            es.addEventListener('change', () => void load());
            es.onerror = () => {
                es?.close();
                retry = setTimeout(() => {
                    void load();
                    connect();
                }, 5000);
            };
        };
        connect();
        const tick = setInterval(() => (now = Date.now()), 1000);
        return () => {
            es?.close();
            clearTimeout(retry);
            clearInterval(tick);
        };
    });

    const serverNow = $derived(now + skew);
    const pills = $derived(
        state_
            ? {
                  addon: addonPill({...state_, now: serverNow}, i18n.language),
                  ssh: sshPill(state_.ssh),
                  transport: transportPill(state_),
              }
            : undefined,
    );
    const blocked = $derived(state_ ? blockReason(state_) : '');

    async function run(name: string, f: () => Promise<void>) {
        busy = name;
        try {
            await f();
        } catch (e) {
            notices.push('error', (e as Error).message);
        } finally {
            busy = '';
        }
    }

    function askEnable() {
        hours = state_?.window ?? 8;
        enabling = true;
    }

    const enable = () =>
        run('switch', async () => {
            state_ = await put<State>('api/v1/enabled', {on: true, hours});
            enabling = false;
            notices.push('warn', t('The root shell is switched on.'));
        });

    const disable = () =>
        run('switch', async () => {
            state_ = await put<State>('api/v1/enabled', {on: false});
            notices.push('info', t('The root shell is switched off. Open sessions were ended.'));
        });

    function openShell() {
        // a tab of its own, without a reference back: a page framed on the system's origin cannot
        // reach into it. Theme and language travel in the address, the terminal has no shell around it.
        const url = new URL('terminal', document.baseURI);
        url.searchParams.set('theme', theme.resolved);
        url.searchParams.set('lang', i18n.language);
        window.open(url.toString(), '_blank', 'noopener');
    }

    const end = (s: Session) =>
        run('end-' + s.id, async () => {
            await del(`api/v1/sessions/${encodeURIComponent(s.id)}`);
            notices.push('info', t('The session was ended.'));
            await load();
        });

    const endAll = () =>
        run('end-all', async () => {
            const r = await del<{ended: number}>('api/v1/sessions');
            notices.push('info', t('{n} sessions were ended.', {n: String(r.ended)}));
            await load();
        });

    const save = () =>
        run('save', async () => {
            state_ = await put<State>('api/v1/settings', form);
            lastStored = {...state_.settings};
            form = {...state_.settings};
            notices.push('info', t('Saved.'));
        });

    const dirty = $derived(!!form && !!state_ && !settingsEqual(form, state_.settings));
    const valid = $derived(!!form && !!state_ && settingsValid(form, state_.bounds));
</script>

{#snippet pill(p: Pill, testId: string)}
    {@const text = t(p.label, p.values)}
    <span class={'sh-pill ' + p.tone} title={text} data-testid={testId}>{text}</span>
{/snippet}

<div class="sh-app" lang={i18n.language}>
    <header class="sh-header">
        <h1>{t('Shell')}</h1>
        <span class="sh-spacer"></span>
        {#if state_}<span class="sh-muted sh-small" data-testid="version">{t('Version {v}', {v: state_.version})}</span>{/if}
        {#if !theme.followsShell}
            <ThemeSwitch theme={theme.choice} onclick={cycleTheme} />
        {/if}
    </header>
    {#if loadError}
        <div class="sh-banner" role="alert">{t('The page could not read the addon\'s state: {error}', {error: loadError})}</div>
    {/if}
    <main class="sh-page">
        {#if state_ && pills && form}
            <p class="sh-intro">{t('A root shell for this system in the browser, for when no ssh client is at hand. The addon itself is not root: each session signs in to the system\'s own SSH server with root\'s password, which is asked every time and stored nowhere. If you have an ssh client, use it.')}</p>

            <section class="sh-card" data-testid="status">
                <h2>{t('Status')}</h2>
                <dl class="sh-rows">
                    <dt>{t('Addon')}</dt>
                    <dd>{@render pill(pills.addon, 'pill-addon')}</dd>
                    <dt>{t('SSH')}</dt>
                    <dd>{@render pill(pills.ssh, 'pill-ssh')}</dd>
                    <dt>{t('Connection')}</dt>
                    <dd>{@render pill(pills.transport, 'pill-transport')}</dd>
                    <dt>{t('Open sessions')}</dt>
                    <dd><span class="sh-count" data-testid="count">{t('{n} of {max}', {n: String(state_.sessions.length), max: String(state_.settings.max_sessions)})}</span></dd>
                </dl>
                <!-- the line that says what blocks: its place is reserved, so the card keeps its height -->
                <p class="sh-hint" data-testid="hint" role="status">
                    {#if blocked}
                        {t(blocked)}
                        {#if state_.ssh !== 'on' && state_.enabled && !state_.blocked}
                            <a href="/system/remote-access#ssh" target="_top">{t('System → Remote access')}</a>
                        {/if}
                    {:else if state_.enabled && state_.by}
                        {t('Switched on by {user}.', {user: state_.by})}
                    {/if}
                </p>
                <div class="sh-actions">
                    {#if state_.enabled}
                        <button class="hmm-button sh-wide" onclick={disable} disabled={busy === 'switch'} data-testid="switch">{t('Switch off')}</button>
                    {:else}
                        <Tooltip text={state_.auth_off ? t(blocked) : ''}>
                            <button class="hmm-button sh-wide sh-danger" onclick={askEnable} disabled={busy === 'switch' || state_.auth_off} data-testid="switch">{t('Switch on')}</button>
                        </Tooltip>
                    {/if}
                    <Tooltip text={blocked ? t(blocked) : t('Opens in a tab of its own.')} testId="open-tip">
                        <button class="hmm-button sh-wide sh-primary" onclick={openShell} disabled={!!blocked} data-testid="open">{t('Open shell')}</button>
                    </Tooltip>
                </div>
            </section>

            <section class="sh-card" data-testid="sessions">
                <div class="sh-card-head">
                    <h2>{t('Sessions')}</h2>
                    <span class="sh-spacer"></span>
                    <button class="hmm-button sh-wide" onclick={endAll} disabled={state_.sessions.length === 0 || busy === 'end-all'} data-testid="end-all">{t('End all')}</button>
                </div>
                <table class="sh-table">
                    <thead>
                        <tr>
                            <th>{t('Account')}</th>
                            <th>{t('From')}</th>
                            <th>{t('Since')}</th>
                            <th>{t('Ends without input in')}</th>
                            <th></th>
                        </tr>
                    </thead>
                    <tbody>
                        {#each state_.sessions as s (s.id)}
                            <tr data-testid="session">
                                <td><span class="sh-cut" title={s.user}>{s.user}</span>{#if s.mine}<span class="sh-mine">{t('yours')}</span>{/if}</td>
                                <td><span class="sh-cut sh-mono" title={s.client}>{s.client}</span></td>
                                <td class="sh-num">{clock(s.since, serverNow, i18n.language)}</td>
                                <td class="sh-num" data-testid="idle">{countdown(Math.min(s.idle_ends, s.ends) - serverNow)}</td>
                                <td class="sh-right"><button class="hmm-button sh-narrow" onclick={() => end(s)} disabled={busy === 'end-' + s.id} data-testid="end">{t('End')}</button></td>
                            </tr>
                        {:else}
                            <tr><td colspan="5" class="sh-muted" data-testid="no-sessions">{t('No session is open.')}</td></tr>
                        {/each}
                    </tbody>
                </table>
            </section>

            <section class="sh-card" data-testid="settings">
                <h2>{t('Settings')}</h2>
                <div class="sh-form">
                    <label for="idle">{t('End a session without input after (minutes)')}</label>
                    <input id="idle" class="hmm-input sh-number" type="number" min={state_.bounds.min.idle_minutes} max={state_.bounds.max.idle_minutes} bind:value={form.idle_minutes} data-testid="idle-minutes" />
                    <span class="sh-muted sh-small">{t('{min} to {max}', {min: String(state_.bounds.min.idle_minutes), max: String(state_.bounds.max.idle_minutes)})}</span>

                    <label for="max">{t('Longest session (hours)')}</label>
                    <input id="max" class="hmm-input sh-number" type="number" min={state_.bounds.min.max_hours} max={state_.bounds.max.max_hours} bind:value={form.max_hours} data-testid="max-hours" />
                    <span class="sh-muted sh-small">{t('{min} to {max}', {min: String(state_.bounds.min.max_hours), max: String(state_.bounds.max.max_hours)})}</span>

                    <label for="count">{t('Sessions at once')}</label>
                    <input id="count" class="hmm-input sh-number" type="number" min={state_.bounds.min.max_sessions} max={state_.bounds.max.max_sessions} bind:value={form.max_sessions} data-testid="max-sessions" />
                    <span class="sh-muted sh-small">{t('{min} to {max}', {min: String(state_.bounds.min.max_sessions), max: String(state_.bounds.max.max_sessions)})}</span>

                    <label for="tunnel">{t('Allow through a tunnel addon')}</label>
                    <input id="tunnel" type="checkbox" bind:checked={form.allow_tunnel} data-testid="allow-tunnel" />
                    <span></span>
                </div>
                <p class="sh-muted sh-small">{t('Output does not count as input: a running command does not keep a session open.')} {t('Off by default: the firewall opens SSH to the local networks only, and a tunnel addon (WireGuard, Tailscale) would carry the shell to every tunnel peer.')}</p>
                <div class="sh-actions">
                    <button class="hmm-button sh-wide sh-primary" onclick={save} disabled={!dirty || !valid || busy === 'save'} data-testid="save">{t('Save')}</button>
                </div>
            </section>

            <section class="sh-card" data-testid="log">
                <h2>{t('Log')}</h2>
                <p class="sh-muted sh-small">{t('Who switched the shell on and off, and who opened and closed a session, since the addon\'s start. The same lines are in the system\'s log. What is typed in a session is never logged.')}</p>
                <div class="sh-log">
                    {#each audit as e (e.at + e.line)}
                        <div class="sh-log-line"><span class="sh-num sh-muted">{clock(e.at, serverNow, i18n.language)}</span> <span class="sh-mono sh-cut" title={e.line}>{e.line}</span></div>
                    {:else}
                        <div class="sh-muted">{t('Nothing yet.')}</div>
                    {/each}
                </div>
            </section>
        {:else if !loadError}
            <p class="sh-muted">{t('Loading…')}</p>
        {/if}
    </main>
    <Notices notices={notices.visible} ondismiss={(id) => notices.dismiss(id)} />
</div>

<Dialog open={enabling} title={t('Switch on the root shell?')} width="32rem" onclose={() => (enabling = false)} testId="enable-dialog">
    <p>{t('This switches on a root shell in the browser. Whoever controls this browser, or learns root\'s password, controls the system and every device paired with it. Use it when ssh is not available, and switch it off afterwards. Changes outside /usr/local are lost with the next system update.')}</p>
    <p class="sh-muted">{t('While it is on, the system\'s Status page says so.')}</p>
    <div class="sh-form sh-form-dialog">
        <label for="hours">{t('Switched on for')}</label>
        <select id="hours" class="hmm-select" bind:value={hours} data-testid="hours">
            {#each state_?.windows ?? [] as w (w)}
                {@const l = windowLabel(w)}
                <option value={w}>{t(l.key, l.values)}</option>
            {/each}
        </select>
    </div>
    {#snippet buttons()}
        <button class="hmm-button" onclick={() => (enabling = false)}>{t('Cancel')}</button>
        <button class="hmm-button sh-danger" onclick={enable} disabled={busy === 'switch'} data-testid="enable-dialog-yes">{t('Switch on')}</button>
    {/snippet}
</Dialog>
