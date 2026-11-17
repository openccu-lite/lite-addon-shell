// The editor, cut down to what the files page needs: Monaco's core with its editing features (find,
// folding, the diff view), the tokenizers of the languages a system's files are written in, and
// one language service - JSON, for validation and formatting. No TypeScript, CSS or HTML service.
// This module is loaded when the first file is opened on a wide screen; a phone gets a plain text
// field instead and never loads it.
//
// The two workers are files of the page itself (Vite builds them from the ?worker imports), so
// the page's policy can stay script-src 'self' with no blob: and no other origin.
import * as monaco from 'monaco-editor/editor/editor.api.js';
import 'monaco-editor/features/register.all.js';
import 'monaco-editor/languages/definitions/css/register.js';
import 'monaco-editor/languages/definitions/dockerfile/register.js';
import 'monaco-editor/languages/definitions/html/register.js';
import 'monaco-editor/languages/definitions/ini/register.js';
import 'monaco-editor/languages/definitions/javascript/register.js';
import 'monaco-editor/languages/definitions/lua/register.js';
import 'monaco-editor/languages/definitions/markdown/register.js';
import 'monaco-editor/languages/definitions/python/register.js';
import 'monaco-editor/languages/definitions/shell/register.js';
import 'monaco-editor/languages/definitions/tcl/register.js';
import 'monaco-editor/languages/definitions/xml/register.js';
import 'monaco-editor/languages/definitions/yaml/register.js';
import 'monaco-editor/languages/features/json/register.js';
import EditorWorker from 'monaco-editor/editor/editor.worker.js?worker';
import JsonWorker from 'monaco-editor/languages/features/json/json.worker.js?worker';

(self as unknown as {MonacoEnvironment: unknown}).MonacoEnvironment = {
    getWorker: (_id: string, label: string) => (label === 'json' ? new JsonWorker() : new EditorWorker()),
};

export {monaco};
