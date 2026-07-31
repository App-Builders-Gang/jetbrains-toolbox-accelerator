#!/bin/sh
# jtaccel installer for macOS and Linux.
#
# Downloads the correct release binary, verifies its SHA-256 against the
# published checksums, installs it to ~/.local/bin (creating it and ensuring it
# is on PATH), then runs `jtaccel install` to wire up Toolbox.
#
# Re-running this script updates to the latest release.
set -eu

REPO="App-Builders-Gang/jetbrains-toolbox-accelerator"
BASE="https://github.com/${REPO}/releases/latest/download"

uname_s="$(uname -s)"
uname_m="$(uname -m)"
case "$uname_s" in
    Darwin) os="darwin" ;;
    Linux)  os="linux"  ;;
    *) echo "Unsupported OS: $uname_s" >&2; exit 1 ;;
esac
case "$uname_m" in
    x86_64|amd64) arch="amd64" ;;
    arm64|aarch64) arch="arm64" ;;
    *) echo "Unsupported architecture: $uname_m" >&2; exit 1 ;;
esac

asset="jtaccel-${os}-${arch}"
sums="${BASE}/SHA256SUMS"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

echo "Downloading ${asset}..."
curl -fsSL -o "${tmpdir}/${asset}" "${BASE}/${asset}"
curl -fsSL -o "${tmpdir}/SHA256SUMS" "${sums}"

# Verify the checksum. grep isolates our line so a partial match can't pass.
expected="$(grep -E "[ /]${asset}\$" "${tmpdir}/SHA256SUMS" | awk '{print $1}' | head -n1)"
if [ -z "$expected" ]; then
    echo "Could not find ${asset} in SHA256SUMS" >&2
    exit 1
fi
actual="$(command -v sha256sum >/dev/null 2>&1 && sha256sum "${tmpdir}/${asset}" | awk '{print $1}' || shasum -a 256 "${tmpdir}/${asset}" | awk '{print $1}')"
if [ "$expected" != "$actual" ]; then
    echo "Checksum mismatch!" >&2
    echo "  expected $expected" >&2
    echo "  actual   $actual" >&2
    exit 1
fi
echo "Checksum OK."

bindir="${HOME}/.local/bin"
mkdir -p "$bindir"
install -m 0755 "${tmpdir}/${asset}" "${bindir}/jtaccel"

# Ensure ~/.local/bin is on PATH for this shell and future logins.
case ":${PATH}:" in
    *":${bindir}:"*) ;;
    *)
        echo "Adding ${bindir} to PATH"
        for rc in "${HOME}/.zshrc" "${HOME}/.bashrc" "${HOME}/.profile"; do
            if [ -f "$rc" ]; then
                grep -q "${bindir}" "$rc" 2>/dev/null || \
                    printf '\nexport PATH="%s:$PATH"\n' "$bindir" >> "$rc"
                break
            fi
        done
        export PATH="${bindir}:${PATH}"
        ;;
esac

echo
echo "Installed jtaccel to ${bindir}/jtaccel"
echo "Configuring Toolbox..."
jtaccel install

cat <<EOF

Done. Toolbox downloads are now accelerated.

Watch it work:   jtaccel status
Foreground log:  jtaccel run
Undo everything: jtaccel uninstall
EOF
