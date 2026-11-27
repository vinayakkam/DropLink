package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The CSP (desktop/src-tauri/tauri.conf.json) allows only 'self', the
// sidecar on 127.0.0.1 and inline styles. These rules keep the UI inside
// it, and keep it working offline.
var (
	externalURL  = regexp.MustCompile(`https?://(?:[a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}`)
	inlineScript = regexp.MustCompile(`(?is)<script(?:\s[^>]*)?>\s*[^<\s]`)
	handlerAttr  = regexp.MustCompile(`(?i)\son[a-z]+\s*=\s*["']`)
	dynamicCode  = regexp.MustCompile(`\beval\s*\(|new\s+Function\s*\(`)
)

func uiFiles(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (p == "tests" || (p != "." && strings.HasPrefix(d.Name(), "."))) {
			return filepath.SkipDir
		}
		switch filepath.Ext(p) {
		case ".html", ".js", ".css":
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out[filepath.ToSlash(p)] = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("no UI files found")
	}
	return out
}

func TestNoExternalURLs(t *testing.T) {
	for name, body := range uiFiles(t) {
		for _, m := range externalURL.FindAllString(body, -1) {
			if strings.Contains(m, "tauri.localhost") {
				continue
			}
			t.Errorf("%s loads %s; the UI must work offline and within the CSP", name, m)
		}
	}
}

func TestNoInlineScriptsOrHandlers(t *testing.T) {
	for name, body := range uiFiles(t) {
		if strings.HasSuffix(name, ".html") {
			if inlineScript.MatchString(body) {
				t.Errorf("%s has an inline <script>; use a module file", name)
			}
			if m := handlerAttr.FindString(body); m != "" {
				t.Errorf("%s has an inline event handler %q", name, m)
			}
		}
		if strings.HasSuffix(name, ".js") && dynamicCode.MatchString(body) {
			t.Errorf("%s evaluates code at run time", name)
		}
	}
}

func TestFontLicenseShipsWithFont(t *testing.T) {
	for _, f := range []string{"fonts/monocraft.woff2", "fonts/MONOCRAFT-OFL.txt"} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s missing: %v", f, err)
		}
	}
}
