import {svelte} from '@sveltejs/vite-plugin-svelte';
import {defineConfig} from 'vitest/config';

// The pages are served by the addon's Go backend at /addons/shell/, so every asset URL is
// relative. Two pages: the addon's page (index.html, framed by the system's shell) and the
// terminal (terminal.html, a tab of its own that refuses framing).
export default defineConfig({
    base: './',
    plugins: [svelte()],
    resolve: {dedupe: ['svelte']},
    build: {
        outDir: 'dist',
        emptyOutDir: true,
        target: 'es2022',
        sourcemap: false,
        rollupOptions: {input: {index: 'index.html', terminal: 'terminal.html'}},
    },
    server: {
        // development against a backend started with -no-session-check -insecure-http -port 8290
        proxy: {'/addons/shell/api': {target: 'http://127.0.0.1:8290', ws: true}},
    },
    test: {
        environment: 'jsdom',
        include: ['src/**/*.test.ts'],
    },
});
