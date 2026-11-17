import {svelte} from '@sveltejs/vite-plugin-svelte';
import {defineConfig} from 'vitest/config';

// The pages are served by the addon's Go backend at /addons/shell/, so every asset URL is
// relative. Three pages: the addon's page (index.html, framed by the system's shell), the
// terminal and the files page (terminal.html, files.html: tabs of their own that refuse framing).
// The editor's workers are built as modules of their own, so they load from the page's origin.
export default defineConfig({
    base: './',
    plugins: [svelte()],
    resolve: {dedupe: ['svelte']},
    build: {
        outDir: 'dist',
        emptyOutDir: true,
        target: 'es2022',
        sourcemap: false,
        rollupOptions: {input: {index: 'index.html', terminal: 'terminal.html', files: 'files.html'}},
        // the editor is one large chunk by nature; it is loaded when the first file is opened
        chunkSizeWarningLimit: 6000,
    },
    worker: {format: 'es'},
    server: {
        // development against a backend started with -no-session-check -insecure-http -port 8290
        proxy: {'/addons/shell/api': {target: 'http://127.0.0.1:8290', ws: true}},
    },
    test: {
        environment: 'jsdom',
        include: ['src/**/*.test.ts'],
    },
});
