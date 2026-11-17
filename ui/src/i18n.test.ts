import {readdirSync, readFileSync} from 'node:fs';
import {describe, expect, it} from 'vitest';

import {MESSAGES} from './messages.js';
import {END_REASONS} from './model.js';

type Entry = {de?: string; en?: string};

// every t('…') of the pages and every message key model.ts and fs.ts hand out has its German and English text
describe('messages', () => {
    it('covers every key', () => {
        const dir = process.cwd() + '/src/';
        const missing: string[] = [];
        for (const f of readdirSync(dir).filter((f) => f.endsWith('.svelte'))) {
            const src = readFileSync(dir + f, 'utf8');
            for (const m of src.matchAll(/\bt\((?:'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)")/g)) {
                const key = (m[1] ?? m[2]!).replace(/\\'/g, "'");
                const e = MESSAGES[key] as Entry | undefined;
                if (!e?.de || !e?.en) missing.push(`${f}: ${key}`);
            }
        }
        // the files page hands keys around as plain strings too: a place's label, a button's
        // label, the pill's label, and the sentence that says why a button is blocked
        const files = readFileSync(dir + 'Files.svelte', 'utf8');
        const handed = [/label: '((?:[^'\\]|\\.)+)'/g, /@render act\('((?:[^'\\]|\\.)+)'/g, /'([A-Z](?:[^'\\]|\\.){6,}\.)'/g];
        for (const re of handed) {
            for (const m of files.matchAll(re)) {
                const key = m[1]!.replace(/\\'/g, "'");
                const e = MESSAGES[key] as Entry | undefined;
                if (!e?.de || !e?.en) missing.push(`Files.svelte: ${key}`);
            }
        }
        // model.ts and fs.ts name their keys as label:, key: and plain returned strings
        for (const name of ['model.ts', 'fs.ts']) {
            const model = readFileSync(dir + name, 'utf8');
            for (const m of model.matchAll(/(?:label|key): '((?:[^'\\]|\\.)*)'/g)) {
                const key = m[1]!.replace(/\\'/g, "'");
                const e = MESSAGES[key] as Entry | undefined;
                if (!e?.de || !e?.en) missing.push(`${name}: ${key}`);
            }
            for (const m of model.matchAll(/return '((?:[^'\\]|\\.){12,})';/g)) {
                const key = m[1]!.replace(/\\'/g, "'");
                const e = MESSAGES[key] as Entry | undefined;
                if (!e?.de || !e?.en) missing.push(`${name}: ${key}`);
            }
        }
        for (const r of END_REASONS) if (!MESSAGES['end.' + r]) missing.push('end.' + r);
        expect(missing).toEqual([]);
    });
    it('never says "box", and addresses the user as "Sie"', () => {
        for (const [k, v] of Object.entries(MESSAGES)) {
            const e = v as {de: string; en: string};
            expect(`${k} ${e.de} ${e.en}`).not.toMatch(/\bbox\b/i);
            expect(e.de).not.toMatch(/\b(du|dein|deine|dir|dich)\b/i);
        }
    });
    it('calls the addon "Shell" in both languages, and a session "Shell (root)"', () => {
        expect(MESSAGES['Shell']).toEqual({de: 'Shell', en: 'Shell'});
        expect(MESSAGES['Shell (root)']).toEqual({de: 'Shell (root)', en: 'Shell (root)'});
        for (const [k, v] of Object.entries(MESSAGES)) {
            const e = v as {de: string; en: string};
            expect(`${k} ${e.de} ${e.en}`).not.toMatch(/Terminal & |Root-Sitzung|Root session\b/);
        }
    });
    it('every placeholder of a key is in both texts', () => {
        for (const [k, v] of Object.entries(MESSAGES)) {
            const e = v as {de: string; en: string};
            for (const ph of k.match(/\{[a-z]+\}/g) ?? []) {
                expect(e.de, k).toContain(ph);
                expect(e.en, k).toContain(ph);
            }
        }
    });
});
