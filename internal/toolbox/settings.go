package toolbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Toolbox stores settings with kotlinx.serialization. Field names are the
// @SerialName values, which are snake_case for multi-word properties.
//
// Verified against a value Toolbox itself wrote after entering a proxy in its UI:
//
//	"proxy": { "type": "http", "host": "125.22.22.22", "port": 223 }
//
// Two details are load-bearing:
//
//   - ProxyType is emitted LOWERCASE ("disabled"|"system"|"automatic"|"http"|
//     "socks"). It is built with createAnnotatedEnumSerializer, so the @SerialName
//     wins over the enum constant name. Writing "HTTP" corrupts the file.
//   - Defaulted fields are omitted entirely, so we write only what we set and must
//     preserve every key we did not author.
const (
	KeyProxy   = "proxy"
	KeyNetwork = "network"

	ProxyTypeHTTP     = "http"
	ProxyTypeDisabled = "disabled"

	BackupSuffix = ".jtaccel-backup"
)

// Settings is a loosely-typed view of .settings.json. It is deliberately a map
// rather than a struct: Toolbox owns this schema and adds keys across versions,
// and round-tripping through a struct would silently drop anything we do not know
// about.
type Settings struct {
	raw  map[string]any
	path string
}

func LoadSettings(path string) (*Settings, error) {
	s := &Settings{raw: map[string]any{}, path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(data, &s.raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return s, nil
}

// Save writes the settings back, matching Toolbox's 4-space indentation. The file
// is written atomically so an interrupted install cannot truncate it.
func (s *Settings) Save() error {
	data, err := json.MarshalIndent(s.raw, "", "    ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp := s.path + ".jtaccel-tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Backup copies the current file aside, once. A pre-existing backup is never
// overwritten, so repeated installs cannot destroy the true original.
func (s *Settings) Backup() error {
	backup := s.path + BackupSuffix
	if _, err := os.Stat(backup); err == nil {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(backup), 0o755); err != nil {
		return err
	}
	return os.WriteFile(backup, data, 0o600)
}

// ProxyInfo describes the proxy currently configured in Toolbox.
type ProxyInfo struct {
	Type string
	Host string
	Port int
}

func (s *Settings) Proxy() (ProxyInfo, bool) {
	m, ok := s.raw[KeyProxy].(map[string]any)
	if !ok {
		return ProxyInfo{}, false
	}
	info := ProxyInfo{}
	if v, ok := m["type"].(string); ok {
		info.Type = v
	}
	if v, ok := m["host"].(string); ok {
		info.Host = v
	}
	if v, ok := m["port"].(float64); ok {
		info.Port = int(v)
	}
	return info, true
}

// IsForeignProxy reports whether a proxy is configured that is not ours. Silently
// overwriting a corporate proxy would cut the user off the network, so callers
// must refuse or ask rather than clobber.
func (s *Settings) IsForeignProxy(ourHost string, ourPort int) bool {
	info, ok := s.Proxy()
	if !ok {
		return false
	}
	switch info.Type {
	case "", ProxyTypeDisabled:
		return false
	}
	return !(info.Host == ourHost && info.Port == ourPort)
}

// SetProxy points Toolbox at our listener.
func (s *Settings) SetProxy(host string, port int) {
	s.raw[KeyProxy] = map[string]any{
		"type": ProxyTypeHTTP,
		"host": host,
		"port": port,
	}
}

// SetKeystore registers our PKCS#12 truststore.
//
// Toolbox ADDS these certificates to its defaults
// (CertificateManagerImpl.addCertificatesFromKeystore), so a single-entry store is
// enough and the platform roots keep working. Any sibling keys under "network"
// (connect_timeout, download_timeout) are preserved.
func (s *Settings) SetKeystore(location, password string) {
	net, _ := s.raw[KeyNetwork].(map[string]any)
	if net == nil {
		net = map[string]any{}
	}
	net["keystore"] = map[string]any{
		"location": location,
		"password": password,
	}
	s.raw[KeyNetwork] = net
}

// Keystore returns the configured truststore location, if any.
func (s *Settings) Keystore() (location string, ok bool) {
	net, ok := s.raw[KeyNetwork].(map[string]any)
	if !ok {
		return "", false
	}
	ks, ok := net["keystore"].(map[string]any)
	if !ok {
		return "", false
	}
	loc, ok := ks["location"].(string)
	return loc, ok
}

// Unmanage removes exactly the keys we authored, leaving everything else intact.
// Used by uninstall when no backup is available, and as a belt-and-braces check
// after restoring one.
func (s *Settings) Unmanage() {
	delete(s.raw, KeyProxy)

	if net, ok := s.raw[KeyNetwork].(map[string]any); ok {
		delete(net, "keystore")
		if len(net) == 0 {
			delete(s.raw, KeyNetwork)
		} else {
			s.raw[KeyNetwork] = net
		}
	}
}

// RestoreBackup puts the pre-install settings back and removes the backup.
func RestoreBackup(settingsPath string) (bool, error) {
	backup := settingsPath + BackupSuffix
	data, err := os.ReadFile(backup)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(settingsPath, data, 0o600); err != nil {
		return false, err
	}
	return true, os.Remove(backup)
}
