// Package service registers jtaccel to start at login.
//
// Each platform gets its native mechanism rather than a lowest-common-denominator
// hack: a per-user registry Run entry on Windows, a LaunchAgent on macOS, and a
// systemd --user unit on Linux. None of them require administrator rights.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// Label is the reverse-DNS service identifier used by launchd, and the name
	// of the Windows Run entry / systemd unit.
	Label = "com.appbuildersgang.jtaccel"
	Name  = "jtaccel"
)

// Install registers exePath to run at login with the given arguments.
func Install(exePath string, args []string) error {
	switch runtime.GOOS {
	case "windows":
		return installWindows(exePath, args)
	case "darwin":
		return installLaunchd(exePath, args)
	default:
		return installSystemd(exePath, args)
	}
}

// Uninstall removes the autostart registration. It succeeds when nothing is
// registered, so it is safe to call on a partial install.
func Uninstall() error {
	switch runtime.GOOS {
	case "windows":
		return uninstallWindows()
	case "darwin":
		return uninstallLaunchd()
	default:
		return uninstallSystemd()
	}
}

// IsInstalled reports whether autostart is currently registered.
func IsInstalled() bool {
	switch runtime.GOOS {
	case "windows":
		out, err := exec.Command("reg", "query",
			`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", Name).Output()
		return err == nil && strings.Contains(string(out), Name)
	case "darwin":
		p, err := launchAgentPath()
		if err != nil {
			return false
		}
		_, err = os.Stat(p)
		return err == nil
	default:
		p, err := systemdUnitPath()
		if err != nil {
			return false
		}
		_, err = os.Stat(p)
		return err == nil
	}
}

// ---------------------------------------------------------------- windows ---

func installWindows(exePath string, args []string) error {
	cmd := quoteWindows(exePath)
	for _, a := range args {
		cmd += " " + quoteWindows(a)
	}
	// /f overwrites any previous value, making install idempotent.
	return run("reg", "add", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`,
		"/v", Name, "/t", "REG_SZ", "/d", cmd, "/f")
}

func uninstallWindows() error {
	if !IsInstalled() {
		return nil
	}
	return run("reg", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`,
		"/v", Name, "/f")
}

func quoteWindows(s string) string {
	if strings.ContainsAny(s, ` "`) {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

// ----------------------------------------------------------------- darwin ---

func launchAgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist"), nil
}

func installLaunchd(exePath string, args []string) error {
	p, err := launchAgentPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}

	var argXML strings.Builder
	argXML.WriteString("\t\t<string>" + xmlEscape(exePath) + "</string>\n")
	for _, a := range args {
		argXML.WriteString("\t\t<string>" + xmlEscape(a) + "</string>\n")
	}

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
%s	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ProcessType</key>
	<string>Background</string>
</dict>
</plist>
`, Label, argXML.String())

	if err := os.WriteFile(p, []byte(plist), 0o644); err != nil {
		return err
	}
	// bootout first so a reinstall reloads the new definition.
	_ = run("launchctl", "bootout", "gui/"+currentUID()+"/"+Label)
	return run("launchctl", "bootstrap", "gui/"+currentUID(), p)
}

func uninstallLaunchd() error {
	p, err := launchAgentPath()
	if err != nil {
		return err
	}
	_ = run("launchctl", "bootout", "gui/"+currentUID()+"/"+Label)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func currentUID() string { return fmt.Sprint(os.Getuid()) }

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// ------------------------------------------------------------------ linux ---

func systemdUnitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user", Name+".service"), nil
}

func installSystemd(exePath string, args []string) error {
	p, err := systemdUnitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}

	execStart := quoteWindows(exePath)
	for _, a := range args {
		execStart += " " + a
	}

	unit := fmt.Sprintf(`[Unit]
Description=JetBrains Toolbox download accelerator
After=network-online.target

[Service]
Type=simple
ExecStart=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, execStart)

	if err := os.WriteFile(p, []byte(unit), 0o644); err != nil {
		return err
	}
	_ = run("systemctl", "--user", "daemon-reload")
	return run("systemctl", "--user", "enable", "--now", Name+".service")
}

func uninstallSystemd() error {
	p, err := systemdUnitPath()
	if err != nil {
		return err
	}
	_ = run("systemctl", "--user", "disable", "--now", Name+".service")
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = run("systemctl", "--user", "daemon-reload")
	return nil
}

// -------------------------------------------------------------------------- //

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
