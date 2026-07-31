// Command jtaccel accelerates JetBrains Toolbox downloads.
//
// Toolbox downloads single-stream, which on lossy long-haul paths performs far
// below the available bandwidth. jtaccel runs as a local proxy, splits large
// transfers into concurrent ranged requests, and reassembles them byte-exactly so
// Toolbox's own checksum verification still passes.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/App-Builders-Gang/jetbrains-toolbox-accelerator/internal/ca"
	"github.com/App-Builders-Gang/jetbrains-toolbox-accelerator/internal/config"
	"github.com/App-Builders-Gang/jetbrains-toolbox-accelerator/internal/proxy"
	"github.com/App-Builders-Gang/jetbrains-toolbox-accelerator/internal/service"
	"github.com/App-Builders-Gang/jetbrains-toolbox-accelerator/internal/toolbox"
)

// version is overridden at build time via -ldflags.
var version = "dev"

const usage = `jtaccel - JetBrains Toolbox download accelerator

Usage:
  jtaccel install      Configure Toolbox and start at login
  jtaccel uninstall    Undo everything install did
  jtaccel status       Show current state
  jtaccel run          Run the proxy in the foreground
  jtaccel version      Print version

Run 'jtaccel <command> -h' for command flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "install":
		err = cmdInstall(os.Args[2:])
	case "uninstall":
		err = cmdUninstall(os.Args[2:])
	case "status":
		err = cmdStatus(os.Args[2:])
	case "run":
		err = cmdRun(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Printf("jtaccel %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// ------------------------------------------------------------------- run ---

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	port := fs.Int("port", 0, "listen port (default: from config)")
	idle := fs.Duration("idle-timeout", 0, "exit after this long with no traffic (0 = stay resident)")
	daemon := fs.Bool("daemon", false, "run detached: log to file and hide console")
	verbose := fs.Bool("v", false, "verbose logging")
	_ = fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *port != 0 {
		cfg.Port = *port
	}

	var logOut io.Writer = os.Stderr
	if *daemon {
		hideConsole()
		f, err := os.OpenFile(cfg.LogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err == nil {
			logOut = f
			defer f.Close()
		}
	}
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: level}))

	authority, err := ca.Load(cfg.CADir())
	if err != nil {
		return err
	}

	srv := proxy.New(proxy.Config{
		Addr:        cfg.Addr(),
		CA:          authority,
		IdleTimeout: *idle,
		Logger:      log,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return srv.ListenAndServe(ctx)
}

// --------------------------------------------------------------- install ---

func cmdInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	port := fs.Int("port", 0, "listen port")
	force := fs.Bool("force", false, "replace an existing third-party proxy setting")
	noStart := fs.Bool("no-start", false, "do not restart Toolbox afterwards")
	_ = fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *port != 0 {
		cfg.Port = *port
	}

	inst, err := toolbox.Locate()
	if err != nil {
		return fmt.Errorf("%w (set %s to override)", err, toolbox.EnvDirOverride)
	}
	fmt.Println("Toolbox:", inst.Dir)

	settings, err := toolbox.LoadSettings(inst.SettingsPath)
	if err != nil {
		return err
	}

	// Never silently replace a proxy the user or their employer configured:
	// doing so could cut them off the network entirely.
	if settings.IsForeignProxy("127.0.0.1", cfg.Port) && !*force {
		cur, _ := settings.Proxy()
		return fmt.Errorf(
			"Toolbox already uses proxy %s:%d (type %q).\n"+
				"       Re-run with --force to replace it, or clear it in Toolbox first",
			cur.Host, cur.Port, cur.Type)
	}

	authority, err := ca.Load(cfg.CADir())
	if err != nil {
		return err
	}
	if err := authority.WriteTrustStore(cfg.TrustStorePath(), cfg.KeystorePassword); err != nil {
		return err
	}
	fmt.Println("Truststore:", cfg.TrustStorePath())

	// Toolbox flushes in-memory settings over external edits, so it must be down
	// while we write.
	wasRunning := toolbox.IsRunning()
	if wasRunning {
		fmt.Println("Stopping Toolbox...")
		if err := toolbox.Stop(20 * time.Second); err != nil {
			return err
		}
	}

	if err := settings.Backup(); err != nil {
		return fmt.Errorf("back up settings: %w", err)
	}
	settings.SetProxy("127.0.0.1", cfg.Port)
	settings.SetKeystore(cfg.TrustStorePath(), cfg.KeystorePassword)
	if err := settings.Save(); err != nil {
		return err
	}
	fmt.Printf("Settings: proxy 127.0.0.1:%d + keystore registered\n", cfg.Port)

	cfg.InstalledAt = time.Now()
	cfg.Version = version
	if err := cfg.Save(); err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)
	if err := service.Install(exe, []string{"run", "--daemon"}); err != nil {
		return fmt.Errorf("register autostart: %w", err)
	}
	fmt.Println("Autostart: registered")

	if !proxyAlive(cfg.Addr()) {
		if err := spawnDaemon(exe); err != nil {
			return fmt.Errorf("start proxy: %w", err)
		}
		waitForProxy(cfg.Addr(), 5*time.Second)
	}
	if proxyAlive(cfg.Addr()) {
		fmt.Println("Proxy: listening on", cfg.Addr())
	} else {
		fmt.Println("Proxy: NOT listening - start it with 'jtaccel run'")
	}

	if wasRunning && !*noStart {
		if err := inst.Start(); err != nil {
			fmt.Println("note: could not restart Toolbox:", err)
		} else {
			fmt.Println("Toolbox: restarted")
		}
	}

	fmt.Println("\nDone. Toolbox downloads are now accelerated.")
	fmt.Printf("Watch it work:  jtaccel status   (log: %s)\n", cfg.LogPath())
	return nil
}

// ------------------------------------------------------------- uninstall ---

func cmdUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	purge := fs.Bool("purge", false, "also delete the CA and configuration directory")
	_ = fs.Parse(args)

	// Every step tolerates already being undone, so uninstall is safe to run on a
	// partial or repeated install.
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if err := service.Uninstall(); err != nil {
		fmt.Println("note: autostart removal:", err)
	} else {
		fmt.Println("Autostart: removed")
	}

	stopDaemon(cfg.Addr())

	if inst, err := toolbox.Locate(); err == nil {
		wasRunning := toolbox.IsRunning()
		if wasRunning {
			fmt.Println("Stopping Toolbox...")
			_ = toolbox.Stop(20 * time.Second)
		}

		restored, err := toolbox.RestoreBackup(inst.SettingsPath)
		if err != nil {
			return fmt.Errorf("restore settings: %w", err)
		}
		if restored {
			fmt.Println("Settings: restored from backup")
		}
		// Belt and braces: even after restoring, make sure none of our keys
		// survive (e.g. if the backup predates a partial write).
		settings, err := toolbox.LoadSettings(inst.SettingsPath)
		if err == nil {
			settings.Unmanage()
			if err := settings.Save(); err == nil && !restored {
				fmt.Println("Settings: jtaccel keys removed")
			}
		}

		if wasRunning {
			if err := inst.Start(); err == nil {
				fmt.Println("Toolbox: restarted")
			}
		}
	}

	if *purge {
		if err := cfg.Remove(); err != nil {
			return err
		}
		fmt.Println("Config: purged", cfg.Dir())
	} else {
		fmt.Println("Config kept at", cfg.Dir(), "(use --purge to delete)")
	}

	fmt.Println("\nUninstalled.")
	return nil
}

// ---------------------------------------------------------------- status ---

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	_ = fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	fmt.Printf("jtaccel %s (%s/%s)\n\n", version, runtime.GOOS, runtime.GOARCH)
	fmt.Printf("  config dir      %s\n", cfg.Dir())
	fmt.Printf("  proxy           %s  %s\n", cfg.Addr(), yesNo(proxyAlive(cfg.Addr()), "listening", "not running"))
	fmt.Printf("  autostart       %s\n", yesNo(service.IsInstalled(), "registered", "not registered"))

	if authority, err := ca.Load(cfg.CADir()); err == nil {
		c := authority.Certificate()
		left := time.Until(c.NotAfter)
		fmt.Printf("  CA              valid, expires %s (%d days)\n",
			c.NotAfter.Format("2006-01-02"), int(left.Hours()/24))
	} else {
		fmt.Printf("  CA              missing (%v)\n", err)
	}

	if _, err := os.Stat(cfg.TrustStorePath()); err == nil {
		fmt.Printf("  truststore      %s\n", cfg.TrustStorePath())
	} else {
		fmt.Printf("  truststore      missing\n")
	}

	inst, err := toolbox.Locate()
	if err != nil {
		fmt.Printf("  Toolbox         NOT FOUND\n")
		return nil
	}
	fmt.Printf("  Toolbox         %s (%s)\n", inst.Dir, yesNo(toolbox.IsRunning(), "running", "stopped"))

	settings, err := toolbox.LoadSettings(inst.SettingsPath)
	if err != nil {
		fmt.Printf("  settings        unreadable: %v\n", err)
		return nil
	}
	if p, ok := settings.Proxy(); ok {
		match := p.Host == "127.0.0.1" && p.Port == cfg.Port && p.Type == toolbox.ProxyTypeHTTP
		fmt.Printf("  settings.proxy  %s://%s:%d  %s\n", p.Type, p.Host, p.Port,
			yesNo(match, "OK", "NOT pointing at jtaccel"))
	} else {
		fmt.Printf("  settings.proxy  not set\n")
	}
	if loc, ok := settings.Keystore(); ok {
		fmt.Printf("  settings.keystore %s  %s\n", loc,
			yesNo(loc == cfg.TrustStorePath(), "OK", "points elsewhere"))
	} else {
		fmt.Printf("  settings.keystore not set\n")
	}
	return nil
}

// ---------------------------------------------------------------- helpers ---

func yesNo(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

func proxyAlive(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 700*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func waitForProxy(addr string, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if proxyAlive(addr) {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// spawnDaemon starts a detached proxy process that outlives this command.
func spawnDaemon(exe string) error {
	cmd := exec.Command(exe, "run", "--daemon")
	cmd.Stdout, cmd.Stderr, cmd.Stdin = nil, nil, nil
	detach(cmd)
	return cmd.Start()
}

// stopDaemon terminates a running proxy, best effort.
func stopDaemon(addr string) {
	if !proxyAlive(addr) {
		return
	}
	name := "jtaccel"
	if runtime.GOOS == "windows" {
		name = "jtaccel.exe"
		_ = exec.Command("taskkill", "/F", "/IM", name).Run()
	} else {
		_ = exec.Command("pkill", "-f", name+" run").Run()
	}
	for i := 0; i < 20 && proxyAlive(addr); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if !proxyAlive(addr) {
		fmt.Println("Proxy: stopped")
	}
}
