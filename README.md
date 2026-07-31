# jetbrains-toolbox-accelerator — `jtaccel`

**Make JetBrains Toolbox download at full line speed.** Toolbox downloads tools
over a single TCP connection; on many home and international connections that caps
out far below the bandwidth you pay for. `jtaccel` runs as a local proxy that
splits each large download into parallel streams and reassembles them
byte-for-byte, so Toolbox's own checksum verification still passes — **3×–5×
faster**, with no manual steps after install.

> Single static binary. Windows, macOS, Linux · amd64, arm64. No admin/root.

---

## Install

**macOS / Linux**

```sh
curl -fsSL https://github.com/App-Builders-Gang/jetbrains-toolbox-accelerator/releases/latest/download/install.sh | sh
```

**Windows** (PowerShell)

```powershell
irm https://github.com/App-Builders-Gang/jetbrains-toolbox-accelerator/releases/latest/download/install.ps1 | iex
```

That downloads the binary, verifies its SHA-256, installs it, and runs
`jtaccel install` — which points Toolbox at the proxy and registers a local
**trust store** (see [How it stays trusted](#how-it-stays-trusted) below). Restart
Toolbox once and downloads are accelerated. To update, re-run the same command.

## Commands

```
jtaccel install     Configure Toolbox and start the proxy at login
jtaccel uninstall   Undo everything install did (settings, CA, autostart)
jtaccel status      Show whether the proxy, autostart, CA and Toolbox config are healthy
jtaccel run         Run the proxy in the foreground (for debugging)
jtaccel version     Print version
```

## What's slow, and why

JetBrains Toolbox (the app manager, not the IDEs) fetches each installer over **one
TCP connection**. That sounds fine until you measure it on a real connection:

| Path | Measured speed |
|---|---|
| Line capacity (JetBrains CloudFront) | 8.5 MB/s |
| `edgedl.me.gvt1.com` (Android Studio), **1 connection** | **2.3 MB/s** |
| `edgedl.me.gvt1.com`, 6 connections | 7.8 MB/s |

Aggregate bandwidth is fine; **a single stream is not.** The cause is
[loss-based congestion control (CUBIC)](https://en.wikipedia.org/wiki/CUBIC_TCP)
collapsing its window on a slightly-lossy long-haul path — at a measured ~0.24%
retransmit rate the window never recovers and one stream crawls, while several
streams together fill the pipe.

A few dead ends worth naming, because they look like they should work but don't:

- **A JetBrains mirror/proxy** — Android Studio comes from Google's CDN
  (`edgedl.me.gvt1.com`), not JetBrains'. A JetBrains-side setting can't touch it.
- **Changing DNS** — that host is a single anycast IP from *every* public resolver
  tested (Cloudflare, Google, Quad9, OpenDNS, ISP). There is no better edge to pick.
- **Pinning it to `dl.google.com` via the hosts file** — TLS rejects it: that
  frontend won't serve a certificate for the `edgedl` name.

`jtaccel` works because it terminates the connection locally and re-issues the
download as many ranged requests in parallel. Toolbox is unaware; the result is
identical to a normal download.

## How it works

```
Toolbox ──CONNECT──► jtaccel (127.0.0.1:8899)
                         │
                         ├── auth host? ──► blind TCP splice, never decrypted
                         └── otherwise ──► terminate TLS with a local cert
                                 └─ probe: size + Accept-Ranges
                                     ├─ large + rangeable ─► N parallel ranged GETs
                                     │                        └─ ordered reassembly ─► Toolbox
                                     └─ otherwise ─────────► relayed verbatim
```

Acceleration is decided **per response**, not per host, so it works for any CDN or
file size with no allow-list to maintain. JetBrains account/OAuth hosts are
blind-tunneled by default, so credential traffic is never decrypted.

### Why segment size matters

The proxy must write bytes to Toolbox **in order**, so nothing is emitted until the
first segment arrives — and bandwidth is shared across all workers. Measured on an
8.5 MB/s line:

| Segment size | Time-to-first-byte | Throughput |
|---|---|---|
| 8 MiB | ~16 s | 1.55 MB/s (worse than no proxy) |
| **1 MiB** | **0.94 s** | **8.25 MB/s** |

The default is 1 MiB; the unit test `TestAcceleratedTransferIsByteExact` guards it.

## How it stays trusted

The proxy intercepts TLS, so it needs Toolbox to trust a local certificate
authority. Rather than patching Toolbox's bundled `cacerts` (which Toolbox
**replaces on every self-update**, silently reverting any patch), `jtaccel` uses
Toolbox's own first-party mechanism: the `network.keystore` setting, which
**adds** a truststore to Toolbox's defaults without touching them.

- The CA lives in a per-user directory with user-only permissions.
- It is **never** installed into the OS root store — blast radius is Toolbox alone.
- `jtaccel uninstall` removes it and restores the original settings.

## Privacy

- Only large, range-capable downloads are decrypted and split.
- JetBrains authentication endpoints are tunneled blind by default.
- No body is ever logged or persisted; only one-line transfer summaries.

## Verification

The test suite (`go test ./...`) covers the guarantees that matter:

- **Byte-exact reassembly** of an 8 MiB object fetched as 128 parallel segments —
  the SHA-256 matches a sequential download, which is what Toolbox verifies.
- Ranged/resumed requests are preserved end to end.
- Small objects are relayed, not split.
- The local CA mints a leaf that completes a real TLS 1.3 handshake.
- The PKCS#12 truststore round-trips and decodes under a JVM.
- Toolbox settings merge preserves unrelated keys (`advanced`, `shell_scripts`, …)
  and emits the lowercase `http` proxy type Toolbox requires.

## Troubleshooting

- **`jtaccel status` says the proxy is not listening** — start it with
  `jtaccel run` and read the output, or re-run the installer.
- **A download is not accelerated** — only objects above 32 MiB that advertise
  `Accept-Ranges` are split. That covers IDEs, Android Studio and plugins.
- **Smart App Control (Windows, enforcement mode)** may interfere with loopback
  TLS for unsigned, unreputed binaries. If `jtaccel status` shows the proxy up but
  Toolbox cannot connect on a machine with SAC enforced, this is the likely cause;
  it affects all local-TLS tooling, not just jtaccel. Releasing and distributing the
  signed binary resolves it as the binary gains reputation.

## Complementary: BBR2

The deeper fix for single-stream slowness everywhere (browsers, git, docker, not
just Toolbox) is switching the TCP congestion provider away from CUBIC. On a
permissive network this is a one-liner (Linux) or a setting; it's orthogonal to
jtaccel and the two compose. `jtaccel` does not change system TCP settings.

## License

MIT. See [LICENSE](LICENSE).
