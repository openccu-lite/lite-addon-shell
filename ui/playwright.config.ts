import {defineConfig} from '@playwright/test';

// The pages against the daemon, the fake system and the fake sshd (scripts/dev-serve.sh): build
// the pages first (npm run build). Run as `env -u WAYLAND_DISPLAY -u DISPLAY npx playwright test`
// on WSL.
export default defineConfig({
    testDir: 'e2e',
    timeout: 30_000,
    workers: 1,
    use: {baseURL: 'http://127.0.0.1:18290/addons/shell/', locale: 'en-GB', permissions: ['clipboard-read', 'clipboard-write']},
    webServer: {
        command: '../scripts/dev-serve.sh',
        url: 'http://127.0.0.1:18290/addons/shell/api/v1/health',
        reuseExistingServer: false,
        timeout: 120_000,
    },
});
