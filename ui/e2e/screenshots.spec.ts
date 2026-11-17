import {expect, test} from '@playwright/test';

// The readme's screenshots (SCREENSHOTS=1 npx playwright test screenshots): German, light, and
// the terminal and the files page in dark as well. The pages run against scripts/dev-serve.sh: the shell in the
// pictures is the test's stand-in, not a real system's. The header makes the stand-in answer as
// it would behind the system's web server over HTTPS.
test.skip(!process.env.SCREENSHOTS, 'only on demand');
test.use({locale: 'de-DE', viewport: {width: 1180, height: 760}, extraHTTPHeaders: {'X-Forwarded-Proto': 'https', 'X-Forwarded-For': '192.168.1.23'}});

const out = (n: string) => `../docs/screenshots/${n}.png`;

test('screenshots', async ({page}) => {
    await page.addInitScript(() => {
        localStorage.setItem('shell.language', 'de');
        if (!localStorage.getItem('shell.theme')) localStorage.setItem('shell.theme', 'light');
    });
    await page.goto('./');
    await expect(page.getByTestId('pill-ssh')).toBeVisible();
    if ((await page.getByTestId('switch').textContent())?.trim() === 'Ausschalten') await page.getByTestId('switch').click();
    await expect(page.getByTestId('pill-addon')).toHaveText('Aus');
    await page.screenshot({path: out('01-off')});
    await page.getByTestId('switch').click();
    await expect(page.getByTestId('enable-dialog')).toBeVisible();
    await page.screenshot({path: out('02-switch-on')});
    await page.getByTestId('enable-dialog-yes').click();
    await expect(page.getByTestId('pill-addon')).toContainText('Ein bis');

    for (const theme of ['light', 'dark']) {
        await page.goto(`./terminal?theme=${theme}&lang=de`);
        if (theme === 'light') {
            await page.getByTestId('password').fill('root-pass');
            await page.screenshot({path: out('04-sign-in')});
        } else {
            await page.getByTestId('password').fill('root-pass');
        }
        await page.getByTestId('connect').click();
        await expect(page.locator('.xterm-rows')).toContainText('#');
        await page.keyboard.type('id\n');
        await expect(page.locator('.xterm-rows')).toContainText('uid=0(root)');
        await page.keyboard.type('echo "Das Addon selbst ist nie root: die Sitzung ist eine SSH-Anmeldung am System."\n');
        await page.waitForTimeout(300);
        await page.screenshot({path: out(theme === 'light' ? '03-terminal' : '05-terminal-dark')});
        await page.getByTestId('end-session').click();
        await expect(page.getByTestId('bar')).toHaveClass(/ended/);
    }
    // the files page: a configuration file in the editor, then the root file system, dark
    await page.request.post('http://127.0.0.1:18082/dev/fs/reset');
    await page.goto('./files?theme=light&lang=de');
    await page.getByTestId('password').fill('root-pass');
    await page.getByTestId('connect').click();
    await page.getByTestId('place-/usr/local/etc/config').click();
    await page.locator('[data-path="/usr/local/etc/config/rfd.conf"]').click();
    await expect(page.locator('.monaco-editor').first()).toContainText('Listen Port');
    await page.getByTestId('edit').click();
    await page.locator('.monaco-editor').first().click();
    await page.keyboard.press('ControlOrMeta+End');
    await page.keyboard.insertText('Log Level = 1');
    await page.getByTestId('save').click();
    await expect(page.getByTestId('status')).toContainText('Gespeichert');
    await page.waitForTimeout(300);
    await page.screenshot({path: out('06-files')});
    await page.getByTestId('properties').click();
    await expect(page.getByTestId('backup')).toHaveCount(1);
    await page.screenshot({path: out('07-properties')});
    await page.keyboard.press('Escape');
    await page.goto('./files?theme=dark&lang=de');
    await expect(page.getByTestId('tree')).toBeVisible();
    await page.getByTestId('place-/').click();
    await page.locator('[data-path="/etc"]').click();
    await page.locator('[data-path="/etc/hostname"]').click();
    await expect(page.locator('.monaco-editor').first()).toContainText('openccu-lite');
    await page.waitForTimeout(300);
    await page.screenshot({path: out('08-files-read-only-dark')});
    await page.getByTestId('end-session').click();
    await expect(page.getByTestId('bar')).toHaveClass(/ended/);
    await page.goto('./');
    await page.getByTestId('switch').click();
});
