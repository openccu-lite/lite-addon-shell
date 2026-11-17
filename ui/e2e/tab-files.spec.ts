import {expect, test, type Locator, type Page} from '@playwright/test';

// The files page against the daemon, the fake system and the fake sshd on this machine
// (scripts/dev-serve.sh). The fake sshd's SFTP serves a scratch directory seeded like a small
// system: / is read-only by its /proc/mounts, /usr/local is not, /etc/config is the link into it.
// /dev/fs on the fake system changes a file behind the page's back and makes single operations
// fail the way a real system does. The tests run on the one worker, after page.spec.ts; each
// starts from the addon switched on, no session, and the files as seeded.

const FAKE = 'http://127.0.0.1:18082';
const CONF = '/usr/local/etc/config';
const CSRF = {'X-CCU-Addon-Request': '1'};

test.use({viewport: {width: 1280, height: 800}});

const dev = async (page: Page, path: string, data?: string | Buffer) => expect((await page.request.post(`${FAKE}${path}`, data === undefined ? {} : {data})).ok()).toBe(true);
const onDisk = async (page: Page, path: string) => (await page.request.get(`${FAKE}/dev/fs/read?path=${encodeURIComponent(path)}`)).body();
const text = async (page: Page, path: string) => (await onDisk(page, path)).toString('utf8');
const listed = async (page: Page, path: string) => (await (await page.request.get(`${FAKE}/dev/fs/list?path=${encodeURIComponent(path)}`)).text()).split('\n').filter(Boolean);
const german = (page: Page) => page.addInitScript(() => localStorage.setItem('shell.language', 'de'));
const box = async (l: Locator) => {
    const b = (await l.boundingBox())!;
    return {w: Math.round(b.width), h: Math.round(b.height)};
};
const row = (page: Page, path: string) => page.locator(`[data-testid="row"][data-path="${path}"]`);
const settings = {idle_minutes: 15, max_hours: 8, max_sessions: 2, allow_tunnel: false, files_read_only: false};

async function fresh(page: Page) {
    // the addon on, no session, the limits as shipped, the system's files as seeded
    await page.request.delete('api/v1/sessions', {headers: CSRF});
    expect((await page.request.put('api/v1/settings', {headers: CSRF, data: settings})).ok()).toBe(true);
    expect((await page.request.put('api/v1/enabled', {headers: CSRF, data: {on: true, hours: 1}})).ok()).toBe(true);
    await dev(page, '/dev/fs/reset');
}

async function signIn(page: Page, place = CONF) {
    await page.goto('./files');
    await page.getByTestId('password').fill('root-pass');
    await page.getByTestId('connect').click();
    await expect(page.getByTestId('tree')).toBeVisible();
    if (place) {
        await page.getByTestId('goto').fill(place);
        await page.getByTestId('go').click();
        await expect(page.getByTestId('where')).toContainText(place);
    }
}

async function open(page: Page, path: string) {
    await row(page, path).click();
    await expect(page.getByTestId('file-path')).toHaveText(path);
}

/** Replaces the editor's whole text. */
async function retype(page: Page, content: string) {
    await page.locator('.monaco-editor').first().click();
    await page.keyboard.press('ControlOrMeta+A');
    await page.keyboard.press('Delete');
    await page.keyboard.insertText(content);
}

test.beforeEach(async ({page}) => fresh(page));

test.afterAll(async ({browser}) => {
    const page = await browser.newPage({baseURL: 'http://127.0.0.1:18290/addons/shell/'});
    await page.request.delete('api/v1/sessions', {headers: CSRF});
    await page.request.put('api/v1/settings', {headers: CSRF, data: settings});
    await page.request.put('api/v1/enabled', {headers: CSRF, data: {on: false}});
    await page.close();
});

test('the files page refuses framing, runs the editor under script-src \'self\', and keeps the handle out of every address', async ({page}) => {
    const res = await page.request.get('files');
    expect(res.status()).toBe(200);
    const csp = res.headers()['content-security-policy']!;
    expect(csp).toContain("frame-ancestors 'none'");
    expect(csp).toContain("script-src 'self'");
    expect(csp).toContain("worker-src 'self'");
    expect(csp).not.toContain('blob:');
    expect(csp).not.toContain('unsafe-eval');
    expect(res.headers()['x-frame-options']).toBe('DENY');

    const violations: string[] = [];
    page.on('console', (m) => {
        if (/Content Security Policy|Refused to|violat/i.test(m.text())) violations.push(m.text());
    });
    page.on('pageerror', (e) => violations.push('error: ' + e.message));
    await page.addInitScript(() => {
        document.addEventListener('securitypolicyviolation', (e) => console.error('violation: ' + e.violatedDirective + ' ' + e.blockedURI));
    });
    const urls: string[] = [];
    page.on('request', (r) => urls.push(r.url()));
    page.on('websocket', (w) => urls.push(w.url()));

    await signIn(page);
    await open(page, CONF + '/settings.json');
    await expect(page.locator('.monaco-editor').first()).toBeVisible();
    // the tokenizer coloured the file, and the JSON service (a worker) marks a mistake
    await expect(page.locator('.monaco-editor .mtk1, .monaco-editor [class*="mtk"]').first()).toBeVisible();
    await page.getByTestId('edit').click();
    await retype(page, '{"name": }');
    await expect(page.locator('.monaco-editor .squiggly-error').first()).toBeVisible({timeout: 15_000});
    // both workers are files of the page: its origin, never a blob
    const workers = page.workers().map((w) => w.url());
    expect(workers.length).toBeGreaterThan(0);
    for (const w of workers) expect(w).toMatch(/^http:\/\/127\.0\.0\.1:18290\/addons\/shell\/assets\/.+\.js$/);
    // a download too goes without the handle
    await row(page, CONF + '/crontab').click();
    await page.getByTestId('question-yes').click(); // the JSON is not saved: discard
    const download = page.waitForEvent('download');
    await page.getByTestId('download').click();
    const got = await download;
    expect(got.suggestedFilename()).toBe('crontab');
    // its address names a ticket for this one download, not the session
    expect(got.url()).toMatch(/\/fs\/download\/[0-9a-f]{48}$/);
    urls.push(got.url());

    expect(violations).toEqual([]);
    const handle = await page.evaluate(() => JSON.parse(sessionStorage.getItem('shell.files')!).handle as string);
    expect(handle).toMatch(/^[0-9a-f]{64}$/);
    expect(urls.length).toBeGreaterThan(10);
    for (const u of urls) expect(u).not.toContain(handle);
});

test('the sign-in sheet and the bar: "Shell (root)" with the kind beside it, in both languages', async ({page}) => {
    await page.goto('./files');
    const sheet = page.getByTestId('signin');
    await expect(sheet).toContainText('You are opening root\'s access to the system\'s files.');
    await expect(sheet.getByRole('heading', {name: 'Shell (root)'})).toBeVisible();
    const sheetBox = await box(sheet);
    await page.getByTestId('password').fill('wrong');
    await page.getByTestId('connect').click();
    await expect(page.getByTestId('error')).toHaveText('Wrong password. 4 tries left.');
    expect(await box(sheet)).toEqual(sheetBox);
    await page.getByTestId('password').fill('root-pass');
    await page.getByTestId('connect').click();
    const bar = page.getByTestId('bar');
    await expect(bar).toContainText('Shell (root)');
    await expect(page.getByTestId('bar-kind')).toHaveText('Files');
    await expect(page.getByTestId('bar-user')).toHaveText('dev');
    await expect(page.getByTestId('countdown')).toHaveText(/^1[45]:\d\d$/);
    await expect(page).toHaveTitle('Shell (root)');
    const end = await box(page.getByTestId('end-session'));
    // the addon's page lists it as a files session, and counts it against the limit
    const other = await page.context().newPage();
    await other.goto('./');
    await expect(other.getByTestId('session')).toHaveCount(1);
    await expect(other.getByTestId('kind')).toHaveText('Files');
    await expect(other.getByTestId('count')).toHaveText('1 of 2');
    await expect(other.getByTestId('open-files')).toBeEnabled();
    expect(await box(other.getByTestId('open-files'))).toEqual(await box(other.getByTestId('open')));
    await expect(other.getByTestId('log')).toContainText('session-open kind=files user=dev');
    await other.close();

    // German: the same boxes, "Shell (root)" and "Dateien"
    await german(page);
    await page.reload();
    await expect(page.getByTestId('bar-kind')).toHaveText('Dateien');
    await expect(page.getByTestId('bar')).toContainText('Shell (root)');
    await expect(page.getByTestId('end-session')).toHaveText('Sitzung beenden');
    expect(await box(page.getByTestId('end-session'))).toEqual(end);
    await expect(page.locator('body')).not.toContainText(/Root-Sitzung|Terminal & Dateien|Root file access/);
});

test('the tree: folders open in place, links show where they lead, and the read-only root file system is shown as such', async ({page}) => {
    await signIn(page);
    await expect(row(page, CONF + '/rfd.conf')).toBeVisible();
    // folders first, then names
    const names = await page.getByTestId('row').locator('.sh-name').allTextContents();
    expect(names.slice(0, 2)).toEqual(['addon-policy', 'empty']);
    await expect(row(page, CONF + '/hosts-link')).toContainText('→ ../../../../etc/hostname');
    await expect(row(page, CONF + '/broken').locator('.sh-name')).toHaveClass(/broken/);
    await row(page, CONF + '/addon-policy').click();
    await expect(row(page, CONF + '/addon-policy/shell')).toBeVisible();
    await row(page, CONF + '/empty').click();
    await expect(page.getByTestId('tree')).toContainText('Empty.');
    // here everything can be changed
    for (const b of ['new-file', 'new-folder', 'upload']) await expect(page.getByTestId(b)).toBeEnabled();
    await expect(page.getByTestId('tree-reason')).toHaveCount(0);

    // the root file system: a lock on what cannot be changed, none on the user file system or a tmpfs
    await page.getByTestId('place-/').click();
    await expect(page.getByTestId('tree-reason')).toContainText('This part of the system is read-only.');
    await expect(row(page, '/etc').getByTestId('lock')).toBeVisible();
    await expect(row(page, '/proc').getByTestId('lock')).toBeVisible();
    await expect(row(page, '/tmp').getByTestId('lock')).toHaveCount(0);
    for (const b of ['new-file', 'new-folder', 'upload']) {
        await expect(page.getByTestId(b)).toBeDisabled();
        await expect(page.getByTestId(b + '-tip')).toHaveAttribute('data-tooltip', /This part of the system is read-only\. A change here would not survive the next system update\./);
    }
    await row(page, '/etc').click();
    await open(page, '/etc/hostname');
    await expect(page.getByTestId('file-pill')).toHaveText('Read-only');
    await expect(page.getByTestId('edit')).toBeDisabled();
    await expect(page.getByTestId('edit-tip')).toHaveAttribute('data-tooltip', /read-only/);
    await expect(page.getByTestId('file-note')).toContainText('This part of the system is read-only.');
    for (const b of ['rename', 'delete']) await expect(page.getByTestId(b)).toBeDisabled();
    await expect(page.getByTestId('download')).toBeEnabled();
    // /usr is on the root, /usr/local is the user file system: no lock, and it opens for changes
    await row(page, '/usr').click();
    await expect(row(page, '/usr/local').getByTestId('lock')).toHaveCount(0);
    await row(page, '/usr/local').click();
    await expect(page.getByTestId('new-file')).toBeEnabled();
    // /etc/config is a link into the user file system: followed on click, and what is in it can be changed
    await row(page, '/etc/config').click();
    await expect(row(page, '/etc/config/rfd.conf')).toBeVisible();
    await expect(row(page, '/etc/config').getByTestId('lock')).toHaveCount(0);
    await open(page, '/etc/config/rfd.conf');
    await expect(page.getByTestId('edit')).toBeEnabled();
    await expect(page.getByTestId('file-note')).toHaveText('Opened through a link: the file is /usr/local/etc/config/rfd.conf.');
    // the kernel's files
    await page.getByTestId('goto').fill('/proc');
    await page.getByTestId('go').click();
    await expect(page.getByTestId('tree-reason')).toContainText('These files belong to the kernel');
    await open(page, '/proc/version');
    await expect(page.getByTestId('edit')).toBeDisabled();
    // a folder that is not there
    await page.getByTestId('goto').fill('/nowhere');
    await page.getByTestId('go').click();
    await expect(page.getByTestId('tree-error')).toHaveText('No such file or folder (any more).');
});

test('a file opens read-only; Edit, then Save keeps mode, line ends and the byte order mark, and backs up what it replaces', async ({page}) => {
    await signIn(page);
    await open(page, CONF + '/rfd.conf');
    const pill = page.getByTestId('file-pill');
    await expect(pill).toHaveText('Read-only');
    await expect(page.locator('.monaco-editor').first()).toContainText('Listen Port = 32001');
    const boxes = {pill: await box(pill), edit: await box(page.getByTestId('edit')), reload: await box(page.getByTestId('reload')), status: await box(page.getByTestId('status')), head: await box(page.locator('.sh-edit-head'))};
    // read-only means read-only: typing changes nothing
    await page.locator('.monaco-editor').first().click();
    await page.keyboard.type('x');
    await page.getByTestId('edit').click();
    await expect(pill).toHaveText('Editing');
    await expect(page.getByTestId('save')).toBeDisabled();
    // Save stands where Edit stood, in the same box; so do the pill and the status
    expect(await box(page.getByTestId('save'))).toEqual(boxes.edit);
    expect(await box(pill)).toEqual(boxes.pill);
    await retype(page, 'Listen Port = 32002\n');
    await expect(page.getByTestId('save')).toBeEnabled();
    await page.keyboard.press('ControlOrMeta+S');
    await expect(page.getByTestId('status')).toHaveText(/^Saved \d\d:\d\d$/);
    expect(await box(page.getByTestId('status'))).toEqual(boxes.status);
    expect(await box(page.locator('.sh-edit-head'))).toEqual(boxes.head);
    expect(await box(page.getByTestId('reload'))).toEqual(boxes.reload);
    expect(await text(page, CONF + '/rfd.conf')).toBe('Listen Port = 32002\n');
    await expect(page.getByTestId('save')).toBeDisabled();
    // the backup: the old content, 0600 in 0700 folders, with the .nobackup at the top
    const backups = await listed(page, '/usr/local/shell-backups' + CONF);
    expect(backups).toHaveLength(1);
    expect(backups[0]).toMatch(/^rfd\.conf\.\d{8}T\d{6}Z$/);
    expect(await text(page, `/usr/local/shell-backups${CONF}/${backups[0]}`)).toContain('Listen Port = 32001');
    const mode = (p: string) => page.request.get(`${FAKE}/dev/fs/read?path=${encodeURIComponent(p)}`).then((r) => r.headers()['x-mode']);
    expect(await mode(`/usr/local/shell-backups${CONF}/${backups[0]}`)).toBe('-rw-------');
    expect(await listed(page, '/usr/local/shell-backups')).toContain('.nobackup');
    await expect(row(page, CONF + '/rfd.conf')).toContainText('20 B');

    // a file with Windows line ends and a byte order mark: both are still there after a save
    await open(page, CONF + '/windows.ini');
    await page.getByTestId('edit').click();
    await page.locator('.monaco-editor').first().click();
    await page.keyboard.press('ControlOrMeta+End');
    await page.keyboard.insertText('added=1');
    await page.keyboard.press('Enter');
    await page.getByTestId('save').click();
    await expect(page.getByTestId('status')).toHaveText(/^Saved/);
    expect([...(await onDisk(page, CONF + '/windows.ini'))]).toEqual([0xef, 0xbb, 0xbf, ...Buffer.from('[section]\r\nkey=value\r\nadded=1\r\n')]);
    // a file without a last line end keeps none
    await open(page, CONF + '/no-newline.txt');
    await page.getByTestId('edit').click();
    await page.locator('.monaco-editor').first().click();
    await page.keyboard.press('ControlOrMeta+End');
    await page.keyboard.insertText('!');
    await page.getByTestId('save').click();
    await expect(page.getByTestId('status')).toHaveText(/^Saved/);
    expect(await text(page, CONF + '/no-newline.txt')).toBe('no newline at the end!');
    // the audit lines name the path and the backup, never what the file holds
    await page.goto('./');
    const log = page.getByTestId('log');
    await expect(log).toContainText(`file-write id=`);
    await expect(log).toContainText(`path="${CONF}/rfd.conf" bytes=20 backup="/usr/local/shell-backups${CONF}/rfd.conf.`);
    await expect(log).not.toContainText('32002');
});

test('unsaved changes are asked about before they are lost', async ({page}) => {
    await signIn(page);
    await open(page, CONF + '/crontab');
    await page.getByTestId('edit').click();
    await retype(page, 'changed\n');
    await row(page, CONF + '/rfd.conf').click();
    const q = page.getByTestId('question');
    await expect(q).toContainText('The changes to crontab are not saved. Discard them?');
    await page.getByTestId('question-no').click();
    await expect(page.getByTestId('file-path')).toHaveText(CONF + '/crontab');
    await expect(page.locator('.monaco-editor').first()).toContainText('changed');
    await page.getByTestId('reload').click();
    await page.getByTestId('question-yes').click();
    await expect(page.locator('.monaco-editor').first()).toContainText('0 3 * * *');
    await expect(page.getByTestId('file-pill')).toHaveText('Read-only');
    expect(await text(page, CONF + '/crontab')).toContain('0 3 * * *');
});

test('a file that changed on the system: Overwrite, Reload or Compare', async ({page}) => {
    await signIn(page);
    await open(page, CONF + '/rfd.conf');
    await page.getByTestId('edit').click();
    await retype(page, 'mine\n');
    await dev(page, `/dev/fs/write?path=${CONF}/rfd.conf`, 'theirs\n');
    await page.getByTestId('save').click();
    const dlg = page.getByTestId('conflict');
    await expect(dlg).toContainText('rfd.conf is not what it was when you opened it');
    await expect(page.getByTestId('status')).toHaveText('Not saved: The file changed on the system.');
    expect(await text(page, CONF + '/rfd.conf')).toBe('theirs\n');
    // Compare: the system's version on the left, the editor's text on the right
    await page.getByTestId('conflict-compare').click();
    await expect(page.getByTestId('file-pill')).toHaveText('Comparing');
    await expect(page.locator('.monaco-diff-editor')).toBeVisible();
    await expect(page.locator('.monaco-diff-editor .editor.original')).toContainText('theirs');
    await expect(page.locator('.monaco-diff-editor .editor.modified')).toContainText('mine');
    await expect(page.getByTestId('file-note')).toHaveText('Left: the file as it is on the system now. Right: your text.');
    await page.getByTestId('compare-back').click();
    // the text is still there, still unsaved
    await expect(page.locator('.monaco-editor').first()).toContainText('mine');
    await expect(page.getByTestId('save')).toBeEnabled();
    await page.getByTestId('save').click();
    await expect(dlg).toBeVisible();
    await page.getByTestId('conflict-overwrite').click();
    await expect(page.getByTestId('status')).toHaveText(/^Saved/);
    expect(await text(page, CONF + '/rfd.conf')).toBe('mine\n');
    // what was overwritten is in the backups
    const backups = await listed(page, '/usr/local/shell-backups' + CONF);
    expect(await text(page, `/usr/local/shell-backups${CONF}/${backups[0]}`)).toBe('theirs\n');
    // Reload takes the system's version
    await retype(page, 'mine again\n');
    await dev(page, `/dev/fs/write?path=${CONF}/rfd.conf`, 'theirs again\n');
    await page.getByTestId('save').click();
    await page.getByTestId('conflict-reload').click();
    await expect(page.locator('.monaco-editor').first()).toContainText('theirs again');
    await expect(page.getByTestId('file-pill')).toHaveText('Read-only');
});

test('a file that cannot be replaced is written in place, and a full disk loses nothing', async ({page}) => {
    await signIn(page);
    await open(page, CONF + '/rfd.conf');
    await page.getByTestId('edit').click();
    await retype(page, 'in place\n');
    await dev(page, '/dev/fs/fail?op=PosixRename');
    await page.getByTestId('save').click();
    await expect(page.getByTestId('status')).toHaveText(/^Saved \d\d:\d\d, written in place$/);
    expect(await text(page, CONF + '/rfd.conf')).toBe('in place\n');
    // no space: the save fails, says why, and the text stays in the editor
    await dev(page, '/dev/fs/fail?op=Write');
    await retype(page, 'does not fit\n');
    await page.getByTestId('save').click();
    await expect(page.getByTestId('status')).toHaveText('Not saved: No space left on the file system.');
    await expect(page.getByTestId('status')).toHaveClass(/bad/);
    expect(await text(page, CONF + '/rfd.conf')).toBe('in place\n');
    await expect(page.locator('.monaco-editor').first()).toContainText('does not fit');
    await expect(page.getByTestId('save')).toBeEnabled();
    await dev(page, '/dev/fs/fail');
    await page.getByTestId('save').click();
    await expect(page.getByTestId('status')).toHaveText(/^Saved \d\d:\d\d$/);
    expect(await text(page, CONF + '/rfd.conf')).toBe('does not fit\n');
});

test('binary and large files are shown as what they are', async ({page}) => {
    await dev(page, `/dev/fs/write?path=${CONF}/large.log`, Buffer.alloc((2 << 20) + 10, 'line\n'));
    await dev(page, `/dev/fs/write?path=${CONF}/huge.log`, Buffer.alloc((10 << 20) + 10, 'x'));
    await signIn(page);
    await open(page, CONF + '/firmware.bin');
    await expect(page.getByTestId('file-pill')).toHaveText('Binary');
    await expect(page.getByTestId('hex')).toContainText('00000000  7f 45 4c 46 00 01 02 03  62 69 6e 61 72 79 00 ff  .ELF....binary..');
    await expect(page.getByTestId('edit')).toBeDisabled();
    await expect(page.getByTestId('file-note')).toHaveText('This is not a text file. Its first bytes are shown; download it to work on it.');
    // over 2 MB: shown, not edited
    await open(page, CONF + '/large.log');
    await expect(page.locator('.monaco-editor').first()).toBeVisible();
    await expect(page.getByTestId('edit')).toBeDisabled();
    await expect(page.getByTestId('file-note')).toHaveText('Files over 2 MB are shown, not edited. Download the file to work on it.');
    // over 10 MB: not opened, offered as a download
    await open(page, CONF + '/huge.log');
    await expect(page.getByTestId('file-pill')).toHaveText('Too large');
    await expect(page.getByTestId('editor-body')).toContainText('huge.log has 10.0 MB. Files over 10 MB are not opened here.');
    const download = page.waitForEvent('download');
    await page.getByTestId('download-open').click();
    const d = await download;
    expect(d.suggestedFilename()).toBe('huge.log');
});

test('new file, new folder, rename and delete; a folder with something in it wants its name typed', async ({page}) => {
    await signIn(page);
    await page.getByTestId('new-folder').click();
    const naming = page.getByTestId('naming');
    await expect(naming).toContainText('In ' + CONF);
    await page.getByTestId('naming-value').fill('a/b');
    await page.getByTestId('naming-yes').click();
    await expect(page.getByTestId('naming-error')).toHaveText('The system did not take that name or value.');
    await page.getByTestId('naming-value').fill('sub');
    await page.getByTestId('naming-yes').click();
    await expect(row(page, CONF + '/sub')).toBeVisible();
    // the new folder is selected: the next new file goes into it, opens, and is ready to be written
    await expect(page.getByTestId('where')).toContainText(CONF + '/sub');
    await page.getByTestId('new-file').click();
    await page.getByTestId('naming-value').fill('note.txt');
    await page.keyboard.press('Enter');
    await expect(page.getByTestId('file-path')).toHaveText(CONF + '/sub/note.txt');
    await expect(page.getByTestId('file-pill')).toHaveText('Editing');
    await page.locator('.monaco-editor').first().click();
    await page.keyboard.insertText('a note');
    await page.getByTestId('save').click();
    await expect(page.getByTestId('status')).toHaveText(/^Saved/);
    expect(await text(page, CONF + '/sub/note.txt')).toBe('a note');
    // a name that is taken
    await row(page, CONF + '/sub').click();
    await page.getByTestId('new-file').click();
    await page.getByTestId('naming-value').fill('note.txt');
    await page.getByTestId('naming-yes').click();
    await expect(page.getByTestId('naming-error')).toHaveText('That name is taken.');
    await page.keyboard.press('Escape');
    // rename: the path, so it can move; nothing is overwritten
    await row(page, CONF + '/crontab').click();
    await page.getByTestId('rename').click();
    await expect(page.getByTestId('naming-value')).toHaveValue(CONF + '/crontab');
    await page.getByTestId('naming-value').fill(CONF + '/rfd.conf');
    await page.getByTestId('naming-yes').click();
    await expect(page.getByTestId('naming-error')).toHaveText('That name is taken.');
    await page.getByTestId('naming-value').fill(CONF + '/sub/crontab.old');
    await page.getByTestId('naming-yes').click();
    await expect(row(page, CONF + '/sub/crontab.old')).toBeVisible();
    await expect(row(page, CONF + '/crontab')).toHaveCount(0);
    // delete a file: asked once
    await row(page, CONF + '/hm_addons.cfg').click();
    await page.getByTestId('delete').click();
    await expect(page.getByTestId('question')).toContainText('hm_addons.cfg is deleted. There is no way back.');
    await page.getByTestId('question-yes').click();
    await expect(row(page, CONF + '/hm_addons.cfg')).toHaveCount(0);
    // a link goes as itself
    await row(page, CONF + '/hosts-link').click();
    await page.getByTestId('delete').click();
    await expect(page.getByTestId('question')).toContainText('The link hosts-link is deleted, not what it leads to.');
    await page.getByTestId('question-yes').click();
    await expect(row(page, CONF + '/hosts-link')).toHaveCount(0);
    expect(await text(page, '/etc/hostname')).toBe('openccu-lite\n');
    // the folder holds two entries: its name has to be typed
    await row(page, CONF + '/sub').click();
    await page.getByTestId('delete').click();
    await page.getByTestId('question-yes').click();
    const sure = page.getByTestId('question');
    await expect(sure).toContainText('sub holds 2 entries');
    await expect(page.getByTestId('question-yes')).toBeDisabled();
    await page.getByTestId('question-type').fill('su');
    await expect(page.getByTestId('question-yes')).toBeDisabled();
    await page.getByTestId('question-type').fill('sub');
    await page.getByTestId('question-yes').click();
    await expect(row(page, CONF + '/sub')).toHaveCount(0);
    expect(await listed(page, CONF)).not.toContain('sub');
    // the file that was open in it is gone from the editor too
    await expect(page.getByTestId('file-path')).toHaveText('');
    await page.goto('./');
    const log = page.getByTestId('log');
    await expect(log).toContainText(`file-create id=`);
    await expect(log).toContainText(`file-rename id=`);
    await expect(log).toContainText(`from="${CONF}/crontab" to="${CONF}/sub/crontab.old"`);
    await expect(log).toContainText(`path="${CONF}/sub" entries=3 complete=true`);
});

test('upload asks before it replaces, and download hands the file over', async ({page}) => {
    await signIn(page);
    await page.getByTestId('picker').setInputFiles([
        {name: 'one.txt', mimeType: 'text/plain', buffer: Buffer.from('one\n')},
        {name: 'two.bin', mimeType: 'application/octet-stream', buffer: Buffer.from([0, 1, 2, 3])},
    ]);
    await expect(row(page, CONF + '/one.txt')).toBeVisible();
    await expect(row(page, CONF + '/two.bin')).toContainText('4 B');
    expect(await text(page, CONF + '/one.txt')).toBe('one\n');
    expect([...(await onDisk(page, CONF + '/two.bin'))]).toEqual([0, 1, 2, 3]);
    // again: asked, and the replaced version is kept
    await page.getByTestId('picker').setInputFiles([{name: 'one.txt', mimeType: 'text/plain', buffer: Buffer.from('one, again\n')}]);
    await expect(page.getByTestId('question')).toContainText('one.txt exists already. Its present version goes to the backups first');
    await page.getByTestId('question-no').click();
    expect(await text(page, CONF + '/one.txt')).toBe('one\n');
    await page.getByTestId('picker').setInputFiles([{name: 'one.txt', mimeType: 'text/plain', buffer: Buffer.from('one, again\n')}]);
    await page.getByTestId('question-yes').click();
    await expect(row(page, CONF + '/one.txt')).toContainText('11 B');
    expect(await text(page, CONF + '/one.txt')).toBe('one, again\n');
    expect((await listed(page, '/usr/local/shell-backups' + CONF)).filter((n) => n.startsWith('one.txt.'))).toHaveLength(1);
    // dropped onto the tree: into the selected folder
    await row(page, CONF + '/empty').click();
    const dropped = await page.evaluateHandle(() => {
        const dt = new DataTransfer();
        dt.items.add(new File(['dropped\n'], 'dropped.txt', {type: 'text/plain'}));
        return dt;
    });
    await page.getByTestId('tree').dispatchEvent('drop', {dataTransfer: dropped});
    await expect(row(page, CONF + '/empty/dropped.txt')).toBeVisible();
    expect(await text(page, CONF + '/empty/dropped.txt')).toBe('dropped\n');
    // download
    await row(page, CONF + '/two.bin').click();
    const download = page.waitForEvent('download');
    await page.getByTestId('download').click();
    const d = await download;
    expect(d.suggestedFilename()).toBe('two.bin');
    const chunks: Buffer[] = [];
    for await (const c of (await d.createReadStream())!) chunks.push(c as Buffer);
    expect([...Buffer.concat(chunks)]).toEqual([0, 1, 2, 3]);
    // a folder is not downloaded
    await row(page, CONF + '/addon-policy').click();
    await expect(page.getByTestId('download')).toBeDisabled();
    await page.goto('./');
    await expect(page.getByTestId('log')).toContainText(`file-upload id=`);
    await expect(page.getByTestId('log')).toContainText(`file-download id=`);
});

test('properties: mode and owner of the one entry, and the backups with Compare and Restore', async ({page}) => {
    await signIn(page);
    await open(page, CONF + '/rfd.conf');
    await page.getByTestId('edit').click();
    await retype(page, 'second version\n');
    await page.getByTestId('save').click();
    await expect(page.getByTestId('status')).toHaveText(/^Saved/);
    await page.getByTestId('properties').click();
    const dlg = page.getByTestId('props');
    await expect(dlg).toContainText(CONF + '/rfd.conf');
    await expect(page.getByTestId('props-mode')).toHaveValue('0644');
    await expect(page.getByTestId('props-letters')).toHaveText('rw-r--r--');
    await expect(page.getByTestId('props-apply')).toBeDisabled();
    // the boxes and the digits are one thing
    await page.getByTestId('bit-3').uncheck();
    await page.getByTestId('bit-6').uncheck();
    await expect(page.getByTestId('props-mode')).toHaveValue('0600');
    await page.getByTestId('props-mode').fill('0640');
    await expect(page.getByTestId('bit-3')).toBeChecked();
    await expect(page.getByTestId('props-letters')).toHaveText('rw-r-----');
    await page.getByTestId('props-owner').selectOption({label: 'www-data (33)'});
    await page.getByTestId('props-group').selectOption({label: 'nogroup (65534)'});
    await page.getByTestId('props-apply').click();
    await expect(dlg).toBeHidden();
    await expect(page.getByTestId('selection-detail')).toContainText('0640 rw-r----- · www-data:nogroup');
    // a save after that keeps both
    await retype(page, 'third version\n');
    await page.getByTestId('save').click();
    await expect(page.getByTestId('status')).toHaveText(/^Saved/);
    await expect(page.getByTestId('selection-detail')).toContainText('0640 rw-r----- · www-data:nogroup');
    // the backups: newest first; Compare puts one beside the editor's text
    await page.getByTestId('properties').click();
    await expect(page.getByTestId('backup')).toHaveCount(2);
    await expect(dlg).toContainText('the last 10 of each file, 50 MB in all, in /usr/local/shell-backups');
    await page.getByTestId('backup-compare').last().click();
    await expect(page.locator('.monaco-diff-editor .editor.original')).toContainText('Listen Port = 32001');
    await expect(page.locator('.monaco-diff-editor .editor.modified')).toContainText('third version');
    await expect(page.getByTestId('file-note')).toContainText('Left: the backup of');
    // Restore from the comparison: asked, the present version is kept, the file is the old one again
    await page.getByTestId('compare-restore').click();
    await expect(page.getByTestId('question')).toContainText('rfd.conf is replaced by its version of');
    await page.getByTestId('question-yes').click();
    await expect(page.locator('.monaco-editor').first()).toContainText('Listen Port = 32001');
    expect(await text(page, CONF + '/rfd.conf')).toContain('Listen Port = 32001');
    await page.getByTestId('properties').click();
    await expect(page.getByTestId('backup')).toHaveCount(3);
    // a link has neither mode nor owner of its own
    await page.keyboard.press('Escape');
    await row(page, CONF + '/hosts-link').click();
    await page.getByTestId('properties').click();
    await expect(dlg).toContainText('../../../../etc/hostname');
    await expect(page.getByTestId('props-mode')).toBeDisabled();
    await expect(page.getByTestId('props-error')).toHaveText('A link has no mode or owner of its own.');
    await page.keyboard.press('Escape');
    await page.goto('./');
    await expect(page.getByTestId('log')).toContainText(`file-chmod id=`);
    await expect(page.getByTestId('log')).toContainText(`path="${CONF}/rfd.conf" mode=0640`);
    await expect(page.getByTestId('log')).toContainText(`path="${CONF}/rfd.conf" uid=33 gid=65534`);
    await expect(page.getByTestId('log')).toContainText(`file-restore id=`);
});

test('the setting that makes the files page read-only: everything is shown, nothing can be changed', async ({page}) => {
    await page.goto('./');
    await page.getByTestId('files-read-only').check();
    await page.getByTestId('save').click();
    await expect(page.getByTestId('log')).toContainText('files_read_only=true');
    await signIn(page);
    await expect(page.getByTestId('tree-reason')).toHaveText(/The files page is set to read-only in the addon's settings\./);
    for (const b of ['new-file', 'new-folder', 'upload']) await expect(page.getByTestId(b)).toBeDisabled();
    await open(page, CONF + '/rfd.conf');
    await expect(page.getByTestId('edit')).toBeDisabled();
    await expect(page.getByTestId('edit-tip')).toHaveAttribute('data-tooltip', 'The files page is set to read-only in the addon\'s settings.');
    for (const b of ['rename', 'delete']) await expect(page.getByTestId(b)).toBeDisabled();
    await expect(page.getByTestId('download')).toBeEnabled();
    // and the backend holds the same line
    const id = await page.evaluate(() => JSON.parse(sessionStorage.getItem('shell.files')!) as {id: string; handle: string});
    const res = await page.request.post(`api/v1/sessions/${id.id}/fs/remove`, {headers: {...CSRF, 'X-Shell-Handle': id.handle}, data: {path: CONF + '/rfd.conf'}});
    expect(res.status()).toBe(403);
    expect((await res.json()).error).toBe('read-only-setting');
});

test('nothing changes its box: the buttons, the pill, the status and the selection strip, in both languages', async ({page}) => {
    await signIn(page);
    const ids = ['new-file', 'new-folder', 'upload', 'refresh', 'download', 'rename', 'delete', 'properties', 'edit', 'reload'];
    const measure = async () => {
        const out: Record<string, {w: number; h: number}> = {};
        for (const id of ids) out[id] = await box(page.getByTestId(id));
        out.pill = await box(page.getByTestId('file-pill'));
        out.status = await box(page.getByTestId('status'));
        out.selection = await box(page.getByTestId('selection'));
        out.tree = await box(page.getByTestId('tree'));
        out.note = await box(page.getByTestId('file-note'));
        return out;
    };
    const empty = await measure();
    // the four actions of a row share one width, and so do the two rows
    expect(new Set(['new-file', 'new-folder', 'upload', 'refresh', 'download', 'rename', 'delete', 'properties'].map((i) => empty[i]!.w)).size).toBe(1);
    await open(page, CONF + '/hosts-link');
    expect(await measure()).toEqual(empty);
    await open(page, CONF + '/firmware.bin');
    expect(await measure()).toEqual(empty);
    await page.getByTestId('place-/').click();
    await expect(page.getByTestId('tree-reason')).toBeVisible();
    const locked = await measure();
    expect({...locked, tree: empty.tree}).toEqual(empty);
    // no label is cut in English ...
    for (const id of ids) expect(await page.getByTestId(id).evaluate((el) => el.scrollWidth <= el.clientWidth), id).toBe(true);
    // ... and German has the same boxes, with every label whole
    await german(page);
    await page.reload();
    await expect(page.getByTestId('new-file')).toHaveText('Neue Datei');
    await page.getByTestId('goto').fill(CONF);
    await page.getByTestId('go').click();
    await expect(row(page, CONF + '/rfd.conf')).toBeVisible();
    expect(await measure()).toEqual(empty);
    for (const id of ids) expect(await page.getByTestId(id).evaluate((el) => el.scrollWidth <= el.clientWidth), id).toBe(true);
    await open(page, CONF + '/rfd.conf');
    await expect(page.getByTestId('file-pill')).toHaveText('Nur lesen');
    await page.getByTestId('edit').click();
    await expect(page.getByTestId('file-pill')).toHaveText('Bearbeiten');
    await expect(page.getByTestId('save')).toHaveText('Speichern');
    expect(await box(page.getByTestId('save'))).toEqual(empty.edit);
    expect(await box(page.getByTestId('file-pill'))).toEqual(empty.pill);
    await retype(page, 'x\n');
    await dev(page, '/dev/fs/fail?op=Write');
    await page.getByTestId('save').click();
    // a long status is cut and carries the whole text as its tooltip
    await expect(page.getByTestId('status')).toHaveText('Nicht gespeichert: Kein Platz mehr auf dem Dateisystem.');
    await expect(page.getByTestId('status')).toHaveAttribute('title', 'Nicht gespeichert: Kein Platz mehr auf dem Dateisystem.');
    expect(await box(page.getByTestId('status'))).toEqual(empty.status);
    expect(await box(page.getByTestId('file-pill'))).toEqual(empty.pill);
    await dev(page, '/dev/fs/fail');
});

test('the session ends with the web session, the switch and the addon\'s page; what is in the editor stays to be read', async ({page, context}) => {
    await signIn(page);
    await open(page, CONF + '/rfd.conf');
    await page.getByTestId('edit').click();
    await retype(page, 'not saved yet\n');
    const bar = await box(page.getByTestId('bar'));
    const state = await box(page.getByTestId('bar-state'));
    const end = await box(page.getByTestId('end-session'));
    const other = await context.newPage();
    await other.goto('./');
    await other.getByTestId('end').click();
    await expect(page.getByTestId('bar')).toHaveClass(/ended/);
    await expect(page.getByTestId('bar-state')).toHaveText('Session ended: Ended by you or another administrator.');
    expect(await box(page.getByTestId('bar'))).toEqual(bar);
    expect(await box(page.getByTestId('bar-state'))).toEqual(state);
    expect(await box(page.getByTestId('again'))).toEqual(end);
    // nothing can be done any more, and the text is still there
    await expect(page.locator('.monaco-editor').first()).toContainText('not saved yet');
    for (const b of ['new-file', 'upload', 'refresh', 'download', 'rename', 'delete', 'properties']) await expect(page.getByTestId(b)).toBeDisabled();
    await expect(page.getByTestId('save')).toHaveCount(0);
    await expect(page.getByTestId('edit')).toBeDisabled();
    expect(await text(page, CONF + '/rfd.conf')).toContain('Listen Port = 32001');
    // a new session
    await page.getByTestId('again').click();
    await page.getByTestId('password').fill('root-pass');
    await page.getByTestId('connect').click();
    await expect(page.getByTestId('tree')).toBeVisible();
    // signed out of the system: the files session goes with the web session
    await dev(page, '/dev/session?sid=DEVDEVDEVDEVDEVDEVDEVDEVDE&level=none');
    try {
        await expect(page.getByTestId('bar-state')).toHaveText('Session ended: Your sign-in to the system ended.', {timeout: 10_000});
    } finally {
        await dev(page, '/dev/session?sid=DEVDEVDEVDEVDEVDEVDEVDEVDE&user=dev&level=administer');
    }
    await other.close();
});

test('a reload comes back to the session; a closed tab ends it after the grace', async ({page, context}) => {
    await signIn(page);
    await expect(row(page, CONF + '/rfd.conf')).toBeVisible();
    await page.reload();
    await expect(page.getByTestId('tree')).toBeVisible();
    await expect(page.getByTestId('signin')).toHaveCount(0);
    const other = await context.newPage();
    await other.goto('./');
    await expect(other.getByTestId('session')).toHaveCount(1);
    await page.close();
    await expect(other.getByTestId('session')).toHaveCount(0, {timeout: 15_000});
    await expect(other.getByTestId('log')).toContainText('reason=tab-closed');
    await other.close();
});

test('a phone: a plain text field instead of the editor, one pane at a time, dark, and nothing scrolls sideways', async ({browser}) => {
    const context = await browser.newContext({baseURL: 'http://127.0.0.1:18290/addons/shell/', viewport: {width: 390, height: 800}, hasTouch: true, isMobile: true, colorScheme: 'dark'});
    const page = await context.newPage();
    await page.addInitScript(() => localStorage.setItem('shell.theme', 'dark'));
    const scripts: string[] = [];
    page.on('request', (r) => scripts.push(r.url()));
    await signIn(page);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await expect(page.getByTestId('editor-pane')).toBeHidden();
    await row(page, CONF + '/windows.ini').tap();
    await expect(page.getByTestId('tree-pane')).toBeHidden();
    const area = page.getByTestId('plain-editor');
    await expect(area).toHaveValue('[section]\nkey=value\n');
    await expect(area).toHaveJSProperty('readOnly', true);
    const bg = await area.evaluate((el) => getComputedStyle(el).backgroundColor);
    const [r, g, b] = bg.match(/\d+/g)!.map(Number);
    expect(r! + g! + b!).toBeLessThan(200);
    await page.getByTestId('edit').tap();
    await expect(area).toHaveJSProperty('readOnly', false);
    await area.fill('[section]\nkey=phone\n');
    await page.getByTestId('save').tap();
    await expect(page.getByTestId('status')).toHaveText(/^Saved/);
    // the line ends and the byte order mark of the file, though a text field knows neither
    expect([...(await onDisk(page, CONF + '/windows.ini'))]).toEqual([0xef, 0xbb, 0xbf, ...Buffer.from('[section]\r\nkey=phone\r\n')]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    // the large editor was never fetched
    expect(scripts.filter((u) => /monaco|editor\.worker|json\.worker/.test(u))).toEqual([]);
    await page.getByTestId('back').tap();
    await expect(page.getByTestId('tree-pane')).toBeVisible();
    await context.close();
});
