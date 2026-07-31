// Package config persists jtaccel's own state between runs.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/App-Builders-Gang/jetbrains-toolbox-accelerator/internal/toolbox"
)

const fileName = "config.json"

type Config struct {
	Port int `json:"port"`

	// KeystorePassword protects the PKCS#12 truststore. It is generated per
	// install rather than hardcoded, and lives beside the store in a
	// user-only-readable directory. Toolbox needs it in plaintext in its own
	// settings, so this guards against casual copying, not a local attacker.
	KeystorePassword string `json:"keystore_password"`

	InstalledAt time.Time `json:"installed_at,omitempty"`
	Version     string    `json:"version,omitempty"`

	dir string
}

// Dir returns jtaccel's configuration directory, creating it if needed.
func Dir() (string, error) {
	d, err := toolbox.ConfigDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return d, nil
}

// Load reads config, creating a fresh one (with a new keystore password) if absent.
func Load() (*Config, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	c := &Config{dir: dir}

	data, err := os.ReadFile(filepath.Join(dir, fileName))
	switch {
	case errors.Is(err, os.ErrNotExist):
		c.Port = 8899
		if c.KeystorePassword, err = randomPassword(); err != nil {
			return nil, err
		}
		return c, nil
	case err != nil:
		return nil, err
	}

	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.dir = dir
	if c.Port == 0 {
		c.Port = 8899
	}
	if c.KeystorePassword == "" {
		if c.KeystorePassword, err = randomPassword(); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (c *Config) Save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(c.dir, fileName), append(data, '\n'), 0o600)
}

func (c *Config) Dir() string            { return c.dir }
func (c *Config) CADir() string          { return filepath.Join(c.dir, "ca") }
func (c *Config) TrustStorePath() string { return filepath.Join(c.dir, "truststore.p12") }
func (c *Config) LogPath() string        { return filepath.Join(c.dir, "jtaccel.log") }
func (c *Config) Addr() string           { return fmt.Sprintf("127.0.0.1:%d", c.Port) }

// Remove deletes all persisted state.
func (c *Config) Remove() error {
	return os.RemoveAll(c.dir)
}

func randomPassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
