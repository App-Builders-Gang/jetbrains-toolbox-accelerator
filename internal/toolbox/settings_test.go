package toolbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeSettings(t *testing.T, path, raw string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestSetProxyEmitsLowercaseHTTP locks in the single most fragile detail:
// Toolbox's ProxyType is built with createAnnotatedEnumSerializer, so the value
// is the lowercase @SerialName "http", not the enum constant "HTTP". Getting
// this wrong corrupts the settings file.
func TestSetProxyEmitsLowercaseHTTP(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".settings.json")
	writeSettings(t, path, `{}`)
	s, err := LoadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	s.SetProxy("127.0.0.1", 8899)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(path)
	m := mustJSON(t, string(raw))
	proxy, _ := m["proxy"].(map[string]any)
	if proxy["type"] != "http" {
		t.Fatalf("proxy.type = %v, want \"http\" (lowercase)", proxy["type"])
	}
	if proxy["host"] != "127.0.0.1" || proxy["port"] != float64(8899) {
		t.Fatalf("proxy = %v", proxy)
	}
	// config_url and auth must be absent (defaulted), exactly as Toolbox writes.
	if _, ok := proxy["config_url"]; ok {
		t.Errorf("config_url should be omitted when defaulted")
	}
	if _, ok := proxy["auth"]; ok {
		t.Errorf("auth should be omitted when defaulted")
	}
}

// TestMergePreservesUnknownKeys verifies we never clobber keys we do not own.
// Real users add their own settings (this machine has "advanced"), and
// overwriting the whole file on install would silently destroy them.
func TestMergePreservesUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".settings.json")
	original := `{
    "advanced": {"http_logging_verbosity": "Headers"},
    "shell_scripts": {"location": "/x/scripts"},
    "plugins": {"plugins_auto_updater": true}
}`
	writeSettings(t, path, original)
	s, err := LoadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	s.SetProxy("127.0.0.1", 8899)
	s.SetKeystore("/x/truststore.p12", "pw")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	after := mustJSON(t, func() string {
		b, _ := os.ReadFile(path)
		return string(b)
	}())
	if _, ok := after["advanced"]; !ok {
		t.Errorf("advanced key dropped by merge")
	}
	if _, ok := after["shell_scripts"]; !ok {
		t.Errorf("shell_scripts key dropped by merge")
	}
	if _, ok := after["plugins"]; !ok {
		t.Errorf("plugins key dropped by merge")
	}
	if _, ok := after["proxy"]; !ok {
		t.Errorf("proxy key not added")
	}
}

// TestSetKeystorePreservesNetworkSiblings: connect_timeout and download_timeout
// live alongside keystore under "network" and must survive a write.
func TestSetKeystorePreservesNetworkSiblings(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".settings.json")
	writeSettings(t, path, `{"network":{"connect_timeout":5000,"download_timeout":600000}}`)
	s, _ := LoadSettings(path)
	s.SetKeystore("/x/ts.p12", "pw")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	after := mustJSON(t, func() string { b, _ := os.ReadFile(path); return string(b) }())
	net, _ := after["network"].(map[string]any)
	if net["connect_timeout"] != float64(5000) || net["download_timeout"] != float64(600000) {
		t.Fatalf("network siblings lost: %v", net)
	}
	if net["keystore"] == nil {
		t.Fatalf("keystore not added under network")
	}
}

// TestUnmanageRemovesExactlyOurKeys is the uninstall correctness guarantee.
func TestUnmanageRemovesExactlyOurKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".settings.json")
	writeSettings(t, path, `{
    "advanced": {"x": 1},
    "proxy": {"type": "http", "host": "127.0.0.1", "port": 8899},
    "network": {"keystore": {"location": "/x", "password": "p"}, "connect_timeout": 5}
}`)
	s, _ := LoadSettings(path)
	s.Unmanage()
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	after := mustJSON(t, func() string { b, _ := os.ReadFile(path); return string(b) }())
	if _, ok := after["proxy"]; ok {
		t.Errorf("proxy key not removed")
	}
	net, _ := after["network"].(map[string]any)
	if _, ok := net["keystore"]; ok {
		t.Errorf("keystore not removed")
	}
	if net["connect_timeout"] == nil {
		t.Errorf("unrelated network key removed by Unmanage")
	}
	if _, ok := after["advanced"]; !ok {
		t.Errorf("advanced removed by Unmanage")
	}
}

func TestIsForeignProxy(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".settings.json")
	writeSettings(t, path, `{"proxy":{"type":"http","host":"10.0.0.1","port":8080}}`)
	s, _ := LoadSettings(path)
	if !s.IsForeignProxy("127.0.0.1", 8899) {
		t.Error("corporate proxy should be detected as foreign")
	}

	writeSettings(t, path, `{"proxy":{"type":"disabled"}}`)
	s2, _ := LoadSettings(path)
	if s2.IsForeignProxy("127.0.0.1", 8899) {
		t.Error("disabled proxy should not count as foreign")
	}
}

// TestRoundTripStable verifies an already-managed settings file is idempotent:
// re-installing over an existing install produces the same structure.
func TestRoundTripStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".settings.json")
	writeSettings(t, path, `{"advanced":{"a":1}}`)
	for i := 0; i < 3; i++ {
		s, _ := LoadSettings(path)
		s.SetProxy("127.0.0.1", 8899)
		s.SetKeystore("/x/ts.p12", "pw")
		if err := s.Save(); err != nil {
			t.Fatal(err)
		}
	}
	final := mustJSON(t, func() string { b, _ := os.ReadFile(path); return string(b) }())
	wantProxy := map[string]any{"type": "http", "host": "127.0.0.1", "port": float64(8899)}
	if !reflect.DeepEqual(final["proxy"], wantProxy) {
		t.Fatalf("proxy unstable after re-install: %v", final["proxy"])
	}
}
