<script lang="ts">
    // The sign-in sheet of a root session, the terminal's and the files page's alike: who is
    // signed in to the system, root's password for this one session, and what it opens. The
    // error's place is reserved and the button keeps its width, so the sheet never changes its
    // size.
    import {t} from 'occulite-ui/i18n';

    interface Props {
        /** What the session is: the line under the title says what is being opened. */
        kind: 'terminal' | 'files';
        user: string;
        busy: boolean;
        ready: boolean;
        error: string;
        onconnect: (password: string) => void;
    }
    let {kind, user, busy, ready, error, onconnect}: Props = $props();

    let password = $state('');
    let field = $state<HTMLInputElement | undefined>();

    /** Empties the field and puts the cursor in it (the sheet is shown, or a sign-in failed). */
    export function reset() {
        password = '';
        focus();
    }

    /** Puts the cursor in the field, once it can take it. */
    export function focus() {
        queueMicrotask(() => field?.focus());
    }

    function submit(ev: Event) {
        ev.preventDefault();
        if (!password || busy || !ready) return;
        const p = password;
        password = '';
        onconnect(p);
    }
</script>

<main class="sh-sheet-wrap">
    <form class="sh-sheet" onsubmit={submit} data-testid="signin" autocomplete="off">
        <h1>{t('Shell (root)')}</h1>
        <p>
            {kind === 'files'
                ? t('You are opening root\'s access to the system\'s files. Every session and every change is written to the system\'s log; what a file holds is not.')
                : t('You are opening a root shell on the system. Every session is written to the system\'s log; what you type is not.')}
        </p>
        <div class="sh-form sh-form-sheet">
            <span class="sh-label">{t('Signed in to the system as')}</span>
            <span class="sh-cut sh-strong" title={user} data-testid="account">{user || '\u00a0'}</span>
            <label for="pw">{t('root\'s password')}</label>
            <input id="pw" class="hmm-input" type="password" autocomplete="off" bind:value={password} bind:this={field} disabled={busy || !ready} data-testid="password" />
        </div>
        <!-- the error's place is reserved: the sheet keeps its size -->
        <p class="sh-error" role="alert" data-testid="error">{error}</p>
        <div class="sh-actions">
            <a class="sh-muted sh-small" href="./">{t('To the addon\'s page')}</a>
            <span class="sh-spacer"></span>
            <button class="hmm-button sh-wide sh-primary" type="submit" disabled={busy || !ready || !password} data-testid="connect">{busy ? t('Connecting…') : t('Connect')}</button>
        </div>
        <p class="sh-muted sh-small">{t('The password is asked for every session and is stored nowhere.')}</p>
    </form>
</main>
