import 'occulite-ui/app.css';
import './app.css';
import {mount} from 'svelte';
import {addMessages, initI18n, setLanguage, supportedLanguage} from 'occulite-ui/i18n';
import {initTheme, setTheme, themeOf} from 'occulite-ui/theme';

import Files from './Files.svelte';
import {MESSAGES} from './messages.js';

addMessages(MESSAGES);
// the files page is a tab of its own, without the system's shell around it: the addon's page
// hands it the theme and the language it was shown in (theme= and lang= in the address), and
// they are kept under the addon's own keys for a reload
initI18n({storageKey: 'shell.language'});
initTheme({storageKey: 'shell.theme'});
const params = new URLSearchParams(location.search);
const wantTheme = themeOf(params.get('theme'));
if (wantTheme) setTheme(wantTheme);
const wantLang = supportedLanguage(params.get('lang'));
if (wantLang) setLanguage(wantLang);

mount(Files, {target: document.getElementById('app')!});
