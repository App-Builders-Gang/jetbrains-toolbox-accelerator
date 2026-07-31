package toolbox

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Toolbox keeps settings in memory and flushes them, so an external edit made
// while it is running gets overwritten. Install therefore stops it, edits, and
// starts it again.

func processName() string {
	switch runtime.GOOS {
	case "windows":
		return "jetbrains-toolbox.exe"
	case "darwin":
		return "JetBrains Toolbox"
	default:
		return "jetbrains-toolbox"
	}
}

// IsRunning reports whether Toolbox is currently running.
func IsRunning() bool {
	switch runtime.GOOS {
	case "windows":
		out, err := exec.Command("tasklist", "/FI",
			"IMAGENAME eq "+processName(), "/NH").Output()
		if err != nil {
			return false
		}
		return strings.Contains(strings.ToLower(string(out)), "jetbrains-toolbox")
	default:
		// pgrep exits 1 when nothing matches, which is not an error for us.
		err := exec.Command("pgrep", "-f", processName()).Run()
		return err == nil
	}
}

// Stop asks Toolbox to quit and waits for it to go away. It escalates to a forced
// kill only if a graceful request is ignored.
func Stop(timeout time.Duration) error {
	if !IsRunning() {
		return nil
	}

	switch runtime.GOOS {
	case "windows":
		_ = exec.Command("taskkill", "/IM", processName()).Run()
	case "darwin":
		_ = exec.Command("osascript", "-e", `quit app "JetBrains Toolbox"`).Run()
	default:
		_ = exec.Command("pkill", "-TERM", "-f", processName()).Run()
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !IsRunning() {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}

	switch runtime.GOOS {
	case "windows":
		_ = exec.Command("taskkill", "/F", "/IM", processName()).Run()
	default:
		_ = exec.Command("pkill", "-KILL", "-f", processName()).Run()
	}

	for i := 0; i < 20; i++ {
		if !IsRunning() {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("could not stop %s", processName())
}

// Start launches Toolbox again after settings have been written.
func (i *Install) Start() error {
	switch runtime.GOOS {
	case "windows":
		exe := filepath.Join(i.Dir, "bin", "jetbrains-toolbox.exe")
		return exec.Command(exe).Start()
	case "darwin":
		return exec.Command("open", "-a", "JetBrains Toolbox").Start()
	default:
		exe := filepath.Join(i.Dir, "bin", "jetbrains-toolbox")
		return exec.Command(exe).Start()
	}
}
