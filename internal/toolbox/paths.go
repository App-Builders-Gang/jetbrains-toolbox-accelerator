// Package toolbox locates a JetBrains Toolbox installation and manipulates its
// settings safely.
package toolbox

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// EnvDirOverride lets a user point at a non-standard Toolbox location.
const EnvDirOverride = "JTACCEL_TOOLBOX_DIR"

var ErrNotFound = errors.New("JetBrains Toolbox installation not found")

// Install describes a located Toolbox installation.
type Install struct {
	Dir          string // the Toolbox data directory
	SettingsPath string // .settings.json inside Dir
}

// candidateDirs returns the platform's plausible Toolbox data directories, most
// likely first. These differ per OS and are probed rather than assumed, because
// a wrong guess would silently write settings nothing ever reads.
func candidateDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	var dirs []string

	switch runtime.GOOS {
	case "windows":
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			dirs = append(dirs, filepath.Join(v, "JetBrains", "Toolbox"))
		}
		if home != "" {
			dirs = append(dirs, filepath.Join(home, "AppData", "Local", "JetBrains", "Toolbox"))
		}
	case "darwin":
		if home != "" {
			dirs = append(dirs,
				filepath.Join(home, "Library", "Application Support", "JetBrains", "Toolbox"))
		}
	default: // linux and other unix
		if v := os.Getenv("XDG_DATA_HOME"); v != "" {
			dirs = append(dirs, filepath.Join(v, "JetBrains", "Toolbox"))
		}
		if home != "" {
			dirs = append(dirs,
				filepath.Join(home, ".local", "share", "JetBrains", "Toolbox"))
		}
	}
	return dirs
}

// Locate finds the Toolbox installation, honouring EnvDirOverride first.
func Locate() (*Install, error) {
	if v := os.Getenv(EnvDirOverride); v != "" {
		return newInstall(v), nil
	}
	for _, d := range candidateDirs() {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return newInstall(d), nil
		}
	}
	return nil, ErrNotFound
}

func newInstall(dir string) *Install {
	return &Install{Dir: dir, SettingsPath: filepath.Join(dir, ".settings.json")}
}

// ConfigDir is where jtaccel keeps its CA, truststore and state.
func ConfigDir() (string, error) {
	if v := os.Getenv("JTACCEL_HOME"); v != "" {
		return v, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "jtaccel"), nil
}
