// Package addonpkg tests the package files the addon ships.
package addonpkg

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

type manifest struct {
	Format      int               `json:"format"`
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description map[string]string `json:"description"`
	Release     struct {
		GitHub string `json:"github"`
		Asset  string `json:"asset"`
	} `json:"release"`
	Requires struct {
		Lite          string   `json:"lite"`
		Architectures []string `json:"architectures"`
	} `json:"requires"`
	UI struct {
		SettingsURL   string `json:"settings_url"`
		SessionHeader bool   `json:"session_header"`
	} `json:"ui"`
	Backup  json.RawMessage `json:"backup"`
	Runtime struct {
		Root         bool              `json:"root"`
		Daemon       bool              `json:"daemon"`
		Needs        *[]string         `json:"needs"`
		Capabilities []string          `json:"capabilities"`
		Groups       []string          `json:"groups"`
		Ports        []int             `json:"ports"`
		Paths        []string          `json:"paths"`
		DataDirs     []string          `json:"data_dirs"`
		WebIngress   json.RawMessage   `json:"web_ingress"`
		Bin          string            `json:"bin"`
		APIScopes    []string          `json:"api_scopes"`
		Note         map[string]string `json:"note"`
	} `json:"runtime"`
}

func load(t *testing.T) manifest {
	t.Helper()
	b, err := os.ReadFile("../../addon/openccu-lite.json")
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// The manifest: a confined addon that declares nothing - no root, no capability, no group, no
// port, no path, no web ingress, and no scope on the system's API. That is the addon's whole
// argument: the root is sshd's, never the addon's.
func TestManifest(t *testing.T) {
	m := load(t)
	if m.Format != 1 || m.ID != "shell" || m.Name != "Shell" || !m.Runtime.Daemon || !m.UI.SessionHeader || m.UI.SettingsURL != "/addons/shell/" {
		t.Fatalf("%+v", m)
	}
	rt := m.Runtime
	if rt.Root || len(rt.Capabilities) != 0 || len(rt.Groups) != 0 || len(rt.Ports) != 0 || len(rt.Paths) != 0 || len(rt.DataDirs) != 0 || rt.WebIngress != nil {
		t.Errorf("the addon needs no privilege, no port and no path: %+v", rt)
	}
	if rt.APIScopes == nil || len(rt.APIScopes) != 0 {
		t.Errorf("scopes %v: none - an addon with system:write could switch SSH on or add a key for root by itself", rt.APIScopes)
	}
	if rt.Needs == nil || len(*rt.Needs) != 0 {
		t.Error("needs: [] - the addon waits for no interface process")
	}
	if rt.Bin != "bin" {
		t.Error("runtime.bin: the daemon is root's, not the addon user's to replace")
	}
	if m.Backup != nil {
		t.Error("no backup block: the settings are two small files, and there is no secret to leave out")
	}
	// the session check that leaves the idle timer alone is in openccu-lite from this version on
	if m.Requires.Lite != "2.0.0-beta.7" {
		t.Errorf("requires.lite %q", m.Requires.Lite)
	}
	if strings.Join(m.Requires.Architectures, ",") != "aarch64,x86_64" {
		t.Errorf("architectures %v", m.Requires.Architectures)
	}
	if m.Release.Asset != "shell-{arch}-{version}.tar.gz" || m.Release.GitHub != "openccu-lite/lite-addon-shell" {
		t.Errorf("release %+v", m.Release)
	}
	if m.Description["de"] == "" || m.Description["en"] == "" || rt.Note["de"] == "" || rt.Note["en"] == "" {
		t.Error("the description and the note in both languages")
	}
	// the note says plainly what the addon is, in both directions
	for _, want := range []string{"confined", "not root", "does open a root shell", "not stored", "nothing outside the system", "Status page"} {
		if !strings.Contains(rt.Note["en"], want) {
			t.Errorf("the note does not say %q", want)
		}
	}
	for _, want := range []string{"SSH has to be switched on", "off after the install", "HTTPS only", "administrators only"} {
		if !strings.Contains(m.Description["en"], want) {
			t.Errorf("the description does not say %q", want)
		}
	}
	for _, text := range []string{m.Description["de"], m.Description["en"], rt.Note["de"], rt.Note["en"]} {
		if regexp.MustCompile(`(?i)\bbox\b`).MatchString(text) {
			t.Errorf("user-facing text says box: %s", text)
		}
	}
	for _, text := range []string{m.Description["de"], rt.Note["de"]} {
		if regexp.MustCompile(`(?i)\b(du|dein|deine)\b`).MatchString(text) {
			t.Errorf("German text says du: %s", text)
		}
	}
}

// The manifest's version is the changelog's newest.
func TestVersionInChangelog(t *testing.T) {
	m := load(t)
	cl, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cl), "## ["+m.Version+"]") {
		t.Errorf("CHANGELOG.md has no entry for %s", m.Version)
	}
}

// The manifest's images and the proxy rule are tracked where the build and the catalogue read them.
func TestFilesTracked(t *testing.T) {
	for _, p := range []string{"addon/shell/www/icon.svg", "addon/shell/www/icon-dark.svg", "addon/shell/etc/lighttpd.conf", "addon/shell/www/lighttpd-error-503.html"} {
		if out, err := exec.Command("git", "-C", "../..", "ls-files", "--error-unmatch", "--", p).CombinedOutput(); err != nil {
			t.Errorf("%s not tracked: %v %s", p, err, out)
		}
	}
}

// The proxy rule: the redirect of the bare path, the proxy of /addons/shell/ to the loopback port
// with the WebSocket upgrade, the 503 page - and nothing else. Every directive is one the system
// accepts in an addon's fragment.
func TestFragment(t *testing.T) {
	b, err := os.ReadFile("../../addon/shell/etc/lighttpd.conf")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	want := []string{
		`$HTTP["url"] == "/addons/shell" {`,
		`url.redirect = ("" => "/addons/shell/")`,
		`}`,
		`$HTTP["url"] =~ "^/addons/shell/" {`,
		`proxy.server = ("/addons/shell/" => (("host" => "127.0.0.1", "port" => 8290)))`,
		`proxy.header = ("upgrade" => "enable")`,
		`server.errorfile-prefix = "/usr/local/addons/shell/www/lighttpd-error-"`,
		`}`,
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("etc/lighttpd.conf:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	main, _ := os.ReadFile("../../cmd/lite-shell/main.go")
	if !strings.Contains(string(main), `"port", 8290,`) {
		t.Error("the daemon's default port is not the proxy rule's")
	}
}

// The 503 page names the addon, in the tracked copy and the one the build copies.
func TestErrorPage(t *testing.T) {
	for _, p := range []string{"../../addon/static/lighttpd-error-503.html", "../../addon/shell/www/lighttpd-error-503.html"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "<title>Shell</title>") {
			t.Errorf("%s does not name the addon", p)
		}
	}
}

// The rc.d script and the update_script name no other addon, are sh scripts, and keep the settings.
func TestScripts(t *testing.T) {
	for _, p := range []string{"../../addon/shell/rc.d", "../../addon/update_script"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, other := range []string{"nut", "NUT", "UPS", "upsd", "tailscale", "snmp", "forwarder", "wireguard", "rclone"} {
			if strings.Contains(string(b), other) {
				t.Errorf("%s names another addon: %s", p, other)
			}
		}
		if !strings.HasPrefix(string(b), "#!/bin/sh\n") {
			t.Errorf("%s is not a sh script", p)
		}
		// the limits stay; the one thing removed from the configuration is the switch's state,
		// at the uninstall and at a fresh install: the addon is off after every install
		offs := 0
		for _, l := range strings.Split(string(b), "\n") {
			if !strings.Contains(l, "rm ") || !strings.Contains(l, "$CONF_DIR/addons/$ADDON") || strings.Contains(l, "www") {
				continue
			}
			if strings.TrimSpace(l) == "rm -f $CONF_DIR/addons/$ADDON/state.json" {
				offs++
				continue
			}
			t.Errorf("%s removes the configuration: %s", p, l)
		}
		if offs != 1 {
			t.Errorf("%s: the switch's state must not survive an uninstall or a fresh install (%d)", p, offs)
		}
	}
	rc, _ := os.ReadFile("../../addon/shell/rc.d")
	if !strings.Contains(string(rc), "ADDON=shell\n") || !strings.Contains(string(rc), "DAEMON=$ADDON_DIR/bin/lite-shell\n") || !strings.Contains(string(rc), `DAEMON_ARGS="-addon-dir $ADDON_DIR"`) {
		t.Error("rc.d: the addon name, the daemon or its arguments")
	}
	// bin/ is root's (runtime.bin): the addon's user cannot remove it, and does not try
	for _, l := range strings.Split(string(rc), "\n") {
		if strings.Contains(l, "rm ") && strings.Contains(l, "$ADDON_DIR/bin") {
			t.Errorf("rc.d removes bin/: %s", l)
		}
	}
	us, _ := os.ReadFile("../../addon/update_script")
	if !strings.Contains(string(us), "$ADDON_DIR/bin/.nobackup") {
		t.Error("update_script: the program tree stays out of every backup")
	}
	if !strings.Contains(string(us), "chmod 755 $ADDON_DIR/bin/lite-shell") {
		t.Error("update_script: the daemon's mode")
	}
}
