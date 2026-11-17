<script lang="ts">
    // One file in an editor: Monaco on a wide screen, a plain text field on a phone (Monaco does
    // not work well with a touch keyboard, and a phone should not have to load it). Read-only
    // until the page says otherwise. With `original` set it is a comparison instead: that text on
    // the left, the file's on the right, nothing editable.
    import {onDestroy, onMount, untrack} from 'svelte';
    import {t} from 'occulite-ui/i18n';

    import {forTextarea} from './text.js';

    interface Props {
        /** The text when the file was opened or last saved; a new value replaces the buffer. */
        value: string;
        language: string;
        readOnly: boolean;
        dark: boolean;
        /** The plain text field instead of Monaco. */
        plain: boolean;
        /** Compare: this text on the left. */
        original?: string | undefined;
        /** No minimap and no colouring: a large file, shown only. */
        large?: boolean;
        ondirty?: (dirty: boolean) => void;
        onsave?: () => void;
    }
    let {value, language, readOnly, dark, plain, original = undefined, large = false, ondirty = () => {}, onsave = () => {}}: Props = $props();

    type Monaco = typeof import('./monaco.js').monaco;
    type Editor = import('./monaco.js').monaco.editor.IStandaloneCodeEditor;
    type Diff = import('./monaco.js').monaco.editor.IStandaloneDiffEditor;
    type Model = import('./monaco.js').monaco.editor.ITextModel;

    let host = $state<HTMLDivElement | undefined>();
    let area = $state<HTMLTextAreaElement | undefined>();
    let loadError = $state('');
    let ready = $state(false);
    let m: Monaco | undefined;
    let editor: Editor | undefined;
    let diff: Diff | undefined;
    let model: Model | undefined;
    let originalModel: Model | undefined;
    let cleanVersion = 0;
    let plainText = $state('');
    let plainClean = '';
    let shown: string | undefined;
    let dirty = false;

    /** The buffer as it stands. */
    export function getValue(): string {
        if (plain) return plainText;
        return model?.getValue() ?? value;
    }

    /** The buffer is what is on the system now (after a save). */
    export function markClean() {
        if (plain) plainClean = plainText;
        else cleanVersion = model?.getAlternativeVersionId() ?? 0;
        setDirty(false);
    }

    export function focus() {
        if (plain) area?.focus();
        else (diff ?? editor)?.focus();
    }

    function setDirty(d: boolean) {
        if (d === dirty) return;
        dirty = d;
        ondirty(d);
    }

    const options = () => ({
        readOnly,
        theme: dark ? 'vs-dark' : 'vs',
        automaticLayout: true,
        minimap: {enabled: !large},
        fontSize: 13,
        scrollBeyondLastLine: false,
        renderWhitespace: 'selection' as const,
        // nothing is added to a file behind the user's back
        insertFinalNewline: false,
        trimAutoWhitespace: false,
        largeFileOptimizations: true,
        wordWrap: 'off' as const,
        // the page's policy admits no script from elsewhere, and none is needed
        links: false,
    });

    function teardown() {
        editor?.dispose();
        diff?.dispose();
        model?.dispose();
        originalModel?.dispose();
        editor = diff = undefined;
        model = originalModel = undefined;
    }

    function build() {
        if (!m || !host) return;
        teardown();
        const lang = large ? 'plaintext' : language;
        model = m.editor.createModel(value, lang);
        cleanVersion = model.getAlternativeVersionId();
        model.onDidChangeContent(() => setDirty(model!.getAlternativeVersionId() !== cleanVersion));
        if (original !== undefined) {
            originalModel = m.editor.createModel(original, lang);
            // side by side whatever the width: the note above says "left" and "right"
            diff = m.editor.createDiffEditor(host, {...options(), readOnly: true, originalEditable: false, renderSideBySide: true, useInlineViewWhenSpaceIsLimited: false});
            diff.setModel({original: originalModel, modified: model});
        } else {
            editor = m.editor.create(host, {...options(), model});
            editor.addCommand(m.KeyMod.CtrlCmd | m.KeyCode.KeyS, () => onsave());
        }
        shown = value;
        setDirty(false);
    }

    onMount(async () => {
        if (plain) {
            plainText = plainClean = forTextarea(value);
            shown = value;
            ready = true;
            return;
        }
        try {
            m = (await import('./monaco.js')).monaco;
        } catch (e) {
            loadError = (e as Error).message;
            return;
        }
        ready = true;
        build();
    });

    onDestroy(teardown);

    // a new file, or the comparison switched on or off: a new editor
    $effect(() => {
        void original;
        void language;
        void large;
        const v = value;
        if (!ready) return;
        if (plain) {
            if (v !== shown) {
                plainText = plainClean = forTextarea(v);
                shown = v;
                setDirty(false);
            }
            return;
        }
        // only a new text, a new language or the comparison rebuild the editor: what else build
        // reads (read-only, the theme) is set on the editor that is there, or a session's end
        // would throw away what was typed
        untrack(build);
    });

    $effect(() => {
        const o = options();
        if (!ready || plain) return;
        m?.editor.setTheme(o.theme);
        editor?.updateOptions({readOnly: o.readOnly});
    });

    function plainKey(e: KeyboardEvent) {
        if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
            e.preventDefault();
            onsave();
        }
    }
</script>

{#if loadError}
    <p class="sh-error" role="alert">{t('The editor could not be loaded: {error}', {error: loadError})}</p>
{:else if plain}
    <textarea
        class="sh-plain"
        bind:this={area}
        bind:value={plainText}
        readonly={readOnly || original !== undefined}
        spellcheck="false"
        autocapitalize="off"
        autocomplete="off"
        oninput={() => setDirty(plainText !== plainClean)}
        onkeydown={plainKey}
        data-testid="plain-editor"
        aria-label={t('File content')}
    ></textarea>
{:else}
    <div class="sh-monaco" bind:this={host} data-testid="monaco"></div>
{/if}
