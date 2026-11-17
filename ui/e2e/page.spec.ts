import {expect, test, type Locator, type Page} from '@playwright/test';

// The pages against the daemon, the fake system and the fake sshd on this machine
// (scripts/dev-serve.sh). The tests run in order, on one worker: they share the daemon's switch
// and its sessions. The fake sshd runs a small shell of its own (id, size, echo, flood, exit).

const FAKE = 'http://127.0.0.1:18082';
const SID = 'DEVDEVDEVDEVDEVDEVDEVDEVDE';

test.describe.configure({mode: 'serial'});

const dev = async (page: Page, path: string) => expect((await page.request.post(`${FAKE}${path}`)).ok()).toBe(true);
const german = (page: Page) => page.addInitScript(() => localStorage.setItem('shell.language', 'de'));
const box = async (l: Locator) => {
    const b = (await l.boundingBox())!;
    return {w: Math.round(b.width), h: Math.round(b.height)};
};
const rows = (page: Page) => page.locator('.xterm-rows');

async function switchOn(page: Page, hours?: string) {
    await page.getByTestId('switch').click();
    const dlg = page.getByTestId('enable-dialog');
    if (hours) await dlg.getByTestId('hours').selectOption(hours);
    await dlg.getByTestId('enable-dialog-yes').click();
    await expect(page.getByTestId('pill-addon')).toContainText('On');
}

async function ensureOn(page: Page) {
    await page.goto('./');
    await expect(page.getByTestId('pill-ssh')).toBeVisible();
    if ((await page.getByTestId('switch').textContent())?.trim() === 'Switch on') await switchOn(page);
}

async function signIn(page: Page, password = 'root-pass') {
    await page.goto('./terminal');
    await page.getByTestId('password').fill(password);
    await page.getByTestId('connect').click();
}

async function openShell(page: Page) {
    await signIn(page);
    await expect(page.getByTestId('bar')).toBeVisible();
    await expect(rows(page)).toContainText('#');
}

async function endAll(page: Page) {
    const res = await page.request.delete('api/v1/sessions', {headers: {'X-CCU-Addon-Request': '1'}});
    expect(res.ok()).toBe(true);
}

test('off after the install: the pills say so, the shell cannot be opened, and why', async ({page}) => {
    await page.goto('./');
    await expect(page).toHaveTitle('Shell');
    await expect(page.getByRole('heading', {name: 'Shell', level: 1})).toBeVisible();
    await expect(page.getByTestId('pill-addon')).toHaveText('Off');
    await expect(page.getByTestId('pill-ssh')).toHaveText('On');
    await expect(page.getByTestId('pill-transport')).toHaveText('HTTP');
    await expect(page.getByTestId('count')).toHaveText('0 of 2');
    await expect(page.getByTestId('hint')).toHaveText('The addon is switched off. Switch it on first.');
    await expect(page.getByTestId('open')).toBeDisabled();
    await expect(page.getByTestId('open-tip')).toHaveAttribute('data-tooltip', 'The addon is switched off. Switch it on first.');
    await expect(page.getByTestId('no-sessions')).toBeVisible();
    await expect(page.getByTestId('end-all')).toBeDisabled();
    // a sign-in while it is off is refused by the backend too
    const res = await page.request.post('api/v1/sessions', {headers: {'X-CCU-Addon-Request': '1'}, data: {password: 'root-pass'}});
    expect(res.status()).toBe(409);
    expect((await res.json()).error).toBe('disabled');
});

test('German: the addon is "Shell" there too', async ({page}) => {
    await german(page);
    await page.goto('./');
    await expect(page.getByRole('heading', {name: 'Shell', level: 1})).toBeVisible();
    await expect(page.getByTestId('pill-addon')).toHaveText('Aus');
    await expect(page.getByTestId('switch')).toHaveText('Einschalten');
    await expect(page.getByTestId('open')).toHaveText('Shell öffnen');
    await expect(page.getByTestId('hint')).toHaveText('Das Addon ist ausgeschaltet. Schalten Sie es zuerst ein.');
    await expect(page.locator('body')).not.toContainText(/Terminal & Dateien|Root-Sitzung/);
});

test('switching on asks with the risk and a time limit, and the Status page is told; nothing changes its box', async ({page}) => {
    await page.goto('./');
    const before = {
        pill: await box(page.getByTestId('pill-addon')),
        button: await box(page.getByTestId('switch')),
        open: await box(page.getByTestId('open')),
        card: await box(page.getByTestId('status')),
    };
    await page.getByTestId('switch').click();
    const dlg = page.getByTestId('enable-dialog');
    await expect(dlg).toContainText('Whoever controls this browser, or learns root\'s password, controls the system');
    await expect(dlg.getByTestId('hours')).toHaveValue('8');
    await expect(dlg.getByTestId('hours').locator('option')).toHaveText(['1 hour', '8 hours', 'until I switch it off']);
    await dlg.getByTestId('enable-dialog-yes').click();
    await expect(page.getByTestId('pill-addon')).toHaveText(/^On until (\d\d\/\d\d )?\d\d:\d\d$/);
    await expect(page.getByTestId('switch')).toHaveText('Switch off');
    await expect(page.getByTestId('hint')).toHaveText('Switched on by dev.');
    await expect(page.getByTestId('open')).toBeEnabled();
    expect(await box(page.getByTestId('pill-addon'))).toEqual(before.pill);
    expect(await box(page.getByTestId('switch'))).toEqual(before.button);
    expect(await box(page.getByTestId('open'))).toEqual(before.open);
    expect(await box(page.getByTestId('status'))).toEqual(before.card);
    // the pill of the longest label fits without being cut
    const pill = page.getByTestId('pill-addon');
    expect(await pill.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
    // the system's Status page has the notice, in both languages, ending with the switch's time
    const notices = await (await page.request.get(`${FAKE}/dev/notices`)).json();
    expect(notices.enabled.severity).toBe('warning');
    expect(notices.enabled.title.de).toContain('Root-Shell');
    expect(notices.enabled.title.en).toContain('root shell');
    expect(notices.enabled.ttl).toBeGreaterThan(8 * 3600 - 5);
    expect(notices.enabled.link).toBe('/addon-settings/shell');
    await expect(page.getByTestId('log')).toContainText('enabled until=');
    // in German the boxes are the same
    await german(page);
    await page.reload();
    await expect(page.getByTestId('switch')).toHaveText('Ausschalten');
    await expect(page.getByTestId('pill-addon')).toHaveText(/^Ein bis /);
    expect(await box(page.getByTestId('pill-addon'))).toEqual(before.pill);
    expect(await box(page.getByTestId('switch'))).toEqual(before.button);
    expect(await pill.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
});

test('the terminal page refuses framing and foreign scripts', async ({page}) => {
    const res = await page.request.get('terminal');
    expect(res.status()).toBe(200);
    expect(res.headers()['content-security-policy']).toContain("frame-ancestors 'none'");
    expect(res.headers()['content-security-policy']).toContain("script-src 'self'");
    expect(res.headers()['x-frame-options']).toBe('DENY');
    // and the page works under its own policy: no violation while it loads and runs
    const violations: string[] = [];
    page.on('console', (m) => {
        if (/Content Security Policy|Refused to/.test(m.text())) violations.push(m.text());
    });
    await ensureOn(page);
    await openShell(page);
    expect(violations).toEqual([]);
    await endAll(page);
});

test('a root shell: the sign-in sheet, a wrong password, then id says root', async ({page}) => {
    await ensureOn(page);
    await page.goto('./terminal');
    const sheet = page.getByTestId('signin');
    await expect(sheet).toContainText('You are opening a root shell on the system.');
    await expect(sheet.getByTestId('account')).toHaveText('dev');
    await expect(sheet.getByTestId('connect')).toBeDisabled();
    const before = {sheet: await box(sheet), button: await box(sheet.getByTestId('connect'))};
    await sheet.getByTestId('password').fill('wrong');
    await sheet.getByTestId('connect').click();
    await expect(sheet.getByTestId('error')).toHaveText('Wrong password. 4 tries left.');
    await expect(sheet.getByTestId('password')).toHaveValue('');
    expect(await box(sheet)).toEqual(before.sheet);
    expect(await box(sheet.getByTestId('connect'))).toEqual(before.button);

    await sheet.getByTestId('password').fill('root-pass');
    await sheet.getByTestId('connect').click();
    const bar = page.getByTestId('bar');
    await expect(bar).toContainText('Shell (root)');
    await expect(page).toHaveTitle('Shell (root)');
    await expect(bar.getByTestId('bar-user')).toHaveText('dev');
    await expect(bar).toContainText('from 127.0.0.1');
    await expect(bar.getByTestId('countdown')).toHaveText(/^1[45]:\d\d$/);
    await expect(rows(page)).toContainText('#');
    await page.keyboard.type('id\n');
    await expect(rows(page)).toContainText('uid=0(root) gid=0(root)');
    // the password is in no address and in no storage; the handle is the tab's alone
    expect(page.url()).not.toContain('root-pass');
    const stored = await page.evaluate(() => JSON.stringify({...localStorage}) + JSON.stringify({...sessionStorage}));
    expect(stored).not.toContain('root-pass');
    expect(await page.evaluate(() => document.cookie)).not.toMatch(/[0-9a-f]{64}/);
    // the terminal follows the window
    const cols = async () => {
        const marker = 'm' + Math.random().toString(36).slice(2, 8);
        await page.keyboard.type(`echo ${marker}\nsize\n`);
        const re = new RegExp(`${marker}\\s*#\\s*size\\s*(\\d+) (\\d+)`);
        await expect(rows(page)).toContainText(re);
        return Number((await rows(page).textContent())!.match(re)![1]);
    };
    const first = await cols();
    expect(first).toBeGreaterThan(60);
    await page.setViewportSize({width: 700, height: 500});
    await expect(async () => expect(await cols()).toBeLessThan(first)).toPass({timeout: 8000});
});

test('the addon\'s page lists the session as the caller\'s own, and counts down', async ({page}) => {
    await page.goto('./');
    const row = page.getByTestId('session');
    await expect(row).toHaveCount(1);
    await expect(row).toContainText('dev');
    await expect(row).toContainText('yours');
    await expect(row).toContainText('127.0.0.1');
    await expect(row.getByTestId('idle')).toHaveText(/^1[45]:\d\d$/);
    await expect(page.getByTestId('count')).toHaveText('1 of 2');
    await expect(page.getByTestId('log')).toContainText('session-open kind=terminal user=dev client=127.0.0.1');
    await expect(page.getByTestId('log')).toContainText('login-failed user=dev');
    // what was typed is in no log line
    await expect(page.getByTestId('log')).not.toContainText('uid=0');
    await endAll(page);
    await expect(page.getByTestId('no-sessions')).toBeVisible();
});

test('a paste of several lines asks once per session', async ({page}) => {
    await ensureOn(page);
    await openShell(page);
    const paste = (text: string) =>
        page.evaluate((text) => {
            const dt = new DataTransfer();
            dt.setData('text/plain', text);
            document.querySelector('.xterm-helper-textarea')!.dispatchEvent(new ClipboardEvent('paste', {clipboardData: dt, bubbles: true, cancelable: true}));
        }, text);
    await paste('echo single');
    await expect(rows(page)).toContainText('echo single');
    await expect(page.getByTestId('paste-dialog')).toBeHidden();
    await page.keyboard.press('Enter');
    await paste('echo one\necho two\n');
    const dlg = page.getByTestId('paste-dialog');
    await expect(dlg).toContainText('Paste 2 lines?');
    await expect(dlg).toContainText('Every line runs as root');
    await expect(rows(page)).not.toContainText('echo one');
    await dlg.getByTestId('paste-dialog-yes').click();
    await expect(rows(page)).toContainText('echo two');
    await expect(rows(page)).toContainText(/^.*one.*two/s);
    // asked once: the next one goes through
    await paste('echo three\necho four\n');
    await expect(rows(page)).toContainText('four');
    await expect(page.getByTestId('paste-dialog')).toBeHidden();
});

test('a reload comes back to the same session, with what it showed', async ({page}) => {
    await ensureOn(page);
    await openShell(page);
    await page.keyboard.type('echo before-the-reload\n');
    await expect(rows(page)).toContainText('before-the-reload');
    await page.reload();
    await expect(page.getByTestId('bar')).toContainText('Shell (root)');
    await expect(rows(page)).toContainText('before-the-reload');
    await page.keyboard.type('echo after\n');
    await expect(rows(page)).toContainText('after');
    await page.goto('./');
    await expect(page.getByTestId('session')).toHaveCount(2);
});

test('at most two sessions at once: a third sign-in is refused, and the button says why', async ({page}) => {
    await page.goto('./');
    await expect(page.getByTestId('count')).toHaveText('2 of 2');
    await expect(page.getByTestId('open')).toBeDisabled();
    await expect(page.getByTestId('open-tip')).toHaveAttribute('data-tooltip', 'Too many sessions are open. End one first.');
    await signIn(page);
    await expect(page.getByTestId('error')).toHaveText('Too many sessions are open. End one first.');
    await endAll(page);
});

test('ended on the addon\'s page: the terminal says so, in the same bar, and offers a new session', async ({page, context}) => {
    await ensureOn(page);
    await openShell(page);
    const live = {state: await box(page.getByTestId('bar-state')), button: await box(page.getByTestId('end-session')), bar: await box(page.getByTestId('bar'))};
    const admin = await context.newPage();
    await admin.goto('./');
    await admin.getByTestId('session').getByTestId('end').click();
    await expect(admin.getByTestId('no-sessions')).toBeVisible();
    await expect(page.getByTestId('bar-state')).toHaveText('Session ended: Ended by you or another administrator.');
    await expect(page.getByTestId('bar')).toHaveClass(/ended/);
    expect(await box(page.getByTestId('bar-state'))).toEqual(live.state);
    expect(await box(page.getByTestId('again'))).toEqual(live.button);
    expect(await box(page.getByTestId('bar'))).toEqual(live.bar);
    // what the session showed stays on screen; typing goes nowhere
    await expect(rows(page)).toContainText('#');
    await page.getByTestId('again').click();
    await expect(page.getByTestId('signin')).toBeVisible();
    await expect(page.getByTestId('password')).toBeFocused();
});

test('the shell\'s own exit ends the session', async ({page}) => {
    await ensureOn(page);
    await openShell(page);
    await page.keyboard.type('exit\n');
    await expect(page.getByTestId('bar-state')).toHaveText('Session ended: The shell was left.');
});

test('signed out of the system: the root shell ends with the web session', async ({page}) => {
    await ensureOn(page);
    await openShell(page);
    await dev(page, `/dev/session?sid=${SID}&level=none`);
    try {
        await expect(page.getByTestId('bar-state')).toHaveText('Session ended: Your sign-in to the system ended.', {timeout: 8000});
        // and the watch asked without touching the web session
        const asks = await (await page.request.get(`${FAKE}/dev/asks`)).json();
        expect(asks.peeked[SID]).toBeGreaterThan(0);
        // a new sign-in is refused while the web session is gone
        await page.getByTestId('again').click();
        await page.getByTestId('password').fill('root-pass');
        await page.getByTestId('connect').click();
        await expect(page.getByTestId('error')).toHaveText('You are not signed in to the system as an administrator (any more). Sign in again.');
    } finally {
        await dev(page, `/dev/session?sid=${SID}&user=dev&level=administer`);
    }
});

test('SSH off or keys only: the page says what to do, and open sessions end with SSH', async ({page, context}) => {
    await ensureOn(page);
    const term = await context.newPage();
    await openShell(term);
    try {
        await dev(page, '/dev/sshd?mode=keys-only');
        // what SSH offers is asked at most every two seconds
        await expect(async () => {
            await page.goto('./');
            await expect(page.getByTestId('pill-ssh')).toHaveText('Keys only', {timeout: 1000});
        }).toPass({timeout: 8000});
        await expect(page.getByTestId('hint')).toContainText('SSH accepts keys only, so root\'s password does not work.');
        await expect(page.getByTestId('hint').getByRole('link')).toHaveAttribute('href', '/system/remote-access#ssh');
        await expect(page.getByTestId('open')).toBeDisabled();
        await dev(page, '/dev/sshd?mode=off');
        await expect(term.getByTestId('bar-state')).toHaveText('Session ended: SSH was switched off.', {timeout: 10000});
        await page.reload();
        await expect(page.getByTestId('pill-ssh')).toHaveText('Off');
        await expect(page.getByTestId('hint')).toContainText('SSH is switched off.');
    } finally {
        await dev(page, '/dev/sshd?mode=on');
    }
    await expect(async () => {
        await page.reload();
        await expect(page.getByTestId('pill-ssh')).toHaveText('On', {timeout: 1000});
    }).toPass({timeout: 8000});
});

test('a closed tab: the session ends after the grace', async ({page, context}) => {
    await ensureOn(page);
    const term = await context.newPage();
    await openShell(term);
    await page.goto('./');
    await expect(page.getByTestId('session')).toHaveCount(1);
    await term.close();
    await expect(page.getByTestId('no-sessions')).toBeVisible({timeout: 10000});
    await expect(page.getByTestId('log')).toContainText('reason=tab-closed');
});

test('the settings: the limits within their bounds, saved', async ({page}) => {
    await page.goto('./');
    const save = page.getByTestId('save');
    await expect(save).toBeDisabled();
    await expect(page.getByTestId('idle-minutes')).toHaveValue('15');
    await expect(page.getByTestId('max-hours')).toHaveValue('8');
    await expect(page.getByTestId('max-sessions')).toHaveValue('2');
    await expect(page.getByTestId('allow-tunnel')).not.toBeChecked();
    await page.getByTestId('idle-minutes').fill('61');
    await expect(save).toBeDisabled();
    await page.getByTestId('idle-minutes').fill('5');
    await page.getByTestId('max-sessions').fill('3');
    await expect(save).toBeEnabled();
    await save.click();
    await expect(save).toBeDisabled();
    await expect(page.getByTestId('count')).toHaveText('0 of 3');
    await page.reload();
    await expect(page.getByTestId('idle-minutes')).toHaveValue('5');
    // back to the defaults for the tests after this one
    await page.getByTestId('idle-minutes').fill('15');
    await page.getByTestId('max-sessions').fill('2');
    await page.getByTestId('save').click();
    await expect(page.getByTestId('count')).toHaveText('0 of 2');
});

test('switching off ends the sessions and clears the notice', async ({page, context}) => {
    await ensureOn(page);
    const term = await context.newPage();
    await openShell(term);
    await page.goto('./');
    await page.getByTestId('switch').click();
    await expect(page.getByTestId('pill-addon')).toHaveText('Off');
    await expect(term.getByTestId('bar-state')).toHaveText('Session ended: The addon was switched off.');
    await expect(page.getByTestId('no-sessions')).toBeVisible();
    expect(await (await page.request.get(`${FAKE}/dev/notices`)).json()).toEqual({});
    await expect(page.getByTestId('log')).toContainText('disabled by=dev reason=user sessions_ended=1');
    // the terminal's sign-in says so too
    await term.getByTestId('again').click();
    await term.getByTestId('password').fill('root-pass');
    await term.getByTestId('connect').click();
    await expect(term.getByTestId('error')).toHaveText('The addon is switched off. Switch it on first.');
});

test('without a login on the system the switch is refused', async ({page}) => {
    await dev(page, '/dev/auth-off?on=1');
    try {
        await page.goto('./');
        await expect(page.getByTestId('hint')).toContainText('The system runs without a login');
        await expect(page.getByTestId('switch')).toBeDisabled();
        const res = await page.request.put('api/v1/enabled', {headers: {'X-CCU-Addon-Request': '1'}, data: {on: true, hours: 8}});
        expect(res.status()).toBe(409);
        expect((await res.json()).error).toBe('auth-off');
    } finally {
        await dev(page, '/dev/auth-off?on=0');
    }
});

test('dark mode and a phone: readable, and nothing scrolls sideways', async ({page}) => {
    await page.addInitScript(() => localStorage.setItem('shell.theme', 'dark'));
    await page.setViewportSize({width: 390, height: 800});
    await page.goto('./');
    await expect(page.getByTestId('pill-addon')).toBeVisible();
    const bg = await page.evaluate(() => getComputedStyle(document.querySelector('.sh-app')!).backgroundColor);
    const [r, g, b] = bg.match(/\d+/g)!.map(Number);
    expect(r! + g! + b!).toBeLessThan(200);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await switchOn(page, '1');
    await openShell(page);
    const termBg = await page.evaluate(() => getComputedStyle(document.querySelector('.sh-term')!).backgroundColor);
    expect(termBg).toBe('rgb(13, 17, 23)');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await expect(page.getByTestId('bar')).toContainText('Shell (root)');
    await expect(page.getByTestId('end-session')).toBeVisible();
    await endAll(page);
    await page.goto('./');
    await page.getByTestId('switch').click();
    await expect(page.getByTestId('pill-addon')).toHaveText('Off');
});
