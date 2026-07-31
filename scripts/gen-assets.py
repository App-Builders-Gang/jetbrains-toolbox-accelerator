#!/usr/bin/env python3
"""Generate the README artwork in docs/.

Every illustration exists as a dark/light pair so the README can serve the right
one with <picture media="(prefers-color-scheme: ...)">. Animation is CSS, not SMIL,
so it honours prefers-reduced-motion; the un-animated state of every element is its
final state, so a renderer that ignores the animation still shows a correct picture.

The cube geometry and the three gradients below were measured from the icon that
ships with JetBrains Toolbox (bin/toolbox.ico) — see docs/toolbox-mark.svg.

    python scripts/gen-assets.py
"""
from __future__ import annotations

import os

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', 'docs')

SANS = ("-apple-system,BlinkMacSystemFont,'Segoe UI',Inter,Roboto,"
        "'Helvetica Neue',Arial,sans-serif")
MONO = ("ui-monospace,SFMono-Regular,'SF Mono','Cascadia Code','Roboto Mono',"
        "Menlo,Consolas,monospace")

# Measured Toolbox brand gradient.
BRAND = ('#FF8618', '#FC2771', '#B51EEB')

# Chart palette — validated for both surfaces (lightness band, chroma floor,
# CVD separation, normal-vision floor, contrast) before use.
BASELINE = '#5B8FF9'
ACCEL = '#FC2771'

DARK = dict(
    key='dark',
    bg='#0d1117', panel='#161b22', panel2='#1c2128', track='#21262d',
    border='#30363d', grid='#242c37',
    ink='#e6edf3', ink2='#b6c2cf', muted='#7d8590',
    glow=.34, chip='#1f242c',
)
LIGHT = dict(
    key='light',
    bg='#ffffff', panel='#f6f8fa', panel2='#eff2f5', track='#e9edf1',
    border='#d1d9e0', grid='#e8ecf1',
    ink='#1f2328', ink2='#3d444d', muted='#59636e',
    glow=.13, chip='#f2f4f7',
)


def esc(s: str) -> str:
    return (s.replace('&', '&amp;').replace('<', '&lt;').replace('>', '&gt;'))


# --------------------------------------------------------------- the cube ---

def cube_defs(p: str) -> str:
    """Gradient defs for one cube instance, namespaced by prefix p."""
    def g(name, x1, y1, x2, y2, stops):
        s = ''.join(f'<stop offset="{o}" stop-color="{c}"/>' for o, c in stops)
        return (f'<linearGradient id="{p}{name}" gradientUnits="userSpaceOnUse" '
                f'x1="{x1}" y1="{y1}" x2="{x2}" y2="{y2}">{s}</linearGradient>')
    return (
        g('T', -8.82, 4.57, 155.25, -80.33,
          [(0, '#FF4055'), (.2, '#FC2771'), (.4, '#EA2291'),
           (.6, '#D620B3'), (.8, '#C31FD2'), (1, '#B51EEB')]) +
        g('L', 48.73, -25.21, -57.80, 29.91,
          [(0, '#F6237E'), (.2, '#FD276E'), (.4, '#FF4056'),
           (.6, '#FF5B3E'), (.8, '#FF7626'), (1, '#FF8618')]) +
        g('R', 197.62, 219.02, 121.11, 134.22,
          [(0, '#060008'), (.2, '#0A000B'), (.4, '#130116'),
           (.6, '#220126'), (.8, '#38023E'), (1, '#4A0251')])
    )


def cube_body(p: str, glyph: str = 'jtaccel') -> str:
    """The cube itself, in its native 256x256 space."""
    faces = (f'<polygon points="128,8 232,68 128,128 24,68" fill="url(#{p}T)"/>'
             f'<polygon points="24,68 128,128 128,248 24,188" fill="url(#{p}L)"/>'
             f'<polygon points="128,128 232,68 232,188 128,248" fill="url(#{p}R)"/>')
    if glyph == 'toolbox':
        return faces + '<polygon points="149,199 192,174 192,186 149,211" fill="#fff"/>'
    return faces + (
        '<g fill="#fff">'
        '<polygon points="144.1,149.3 190.4,122.6 190.4,133.2 144.1,159.9"/>'
        '<polygon points="144.1,169.7 182.1,147.8 182.1,158.4 144.1,180.3"/>'
        '<polygon points="144.1,190.1 173.8,173.0 173.8,183.6 144.1,200.7"/>'
        '</g>'
        '<path d="M198.2 119.8 L216.9 133.0 L198.2 167.8" fill="none" stroke="#fff" '
        'stroke-width="11.5" stroke-linecap="round" stroke-linejoin="round"/>')


def cube(x: float, y: float, size: float, p: str, glyph: str = 'jtaccel') -> str:
    """Place a cube with its bounding box top-left at (x, y).

    The drawn shape spans 24..232 / 8..248 inside the 256 box, so it is shifted to
    make `size` the width of the visible cube rather than of the padded canvas.
    """
    s = size / 208.0
    return (f'<g transform="translate({x - 24 * s:.2f},{y - 8 * s:.2f}) scale({s:.5f})">'
            f'{cube_body(p, glyph)}</g>')


# ------------------------------------------------------------- primitives ---

def flow_stripes(x, y, w, h, period, dur, clip_id, opacity=.16, color='#fff'):
    """Diagonal stripes clipped to a bar, translating exactly one period per cycle."""
    lines = []
    n = int(w / period) + 4
    for i in range(-2, n):
        sx = x + i * period
        lines.append(f'<path d="M{sx:.1f} {y} L{sx - h:.1f} {y + h}"/>')
    return (
        f'<clipPath id="{clip_id}"><rect x="{x}" y="{y}" width="{w:.2f}" '
        f'height="{h}" rx="{h / 2}"/></clipPath>'
        f'<g clip-path="url(#{clip_id})">'
        f'<g class="flow" style="--period:{period}px;--dur:{dur}s" '
        f'stroke="{color}" stroke-opacity="{opacity}" stroke-width="6" fill="none">'
        f'{"".join(lines)}</g></g>')


def head(w, h, title, desc, extra_css=''):
    """Open an SVG.

    Fonts and every other static property are presentation attributes, never CSS,
    so a renderer with partial CSS support still lays the picture out correctly.
    CSS carries animation only.
    """
    return (
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" '
        f'height="{h}" role="img" aria-labelledby="ttl dsc">'
        f'<title id="ttl">{esc(title)}</title><desc id="dsc">{esc(desc)}</desc>'
        f'<style>'
        f'@keyframes grow{{from{{transform:scaleX(0)}}to{{transform:scaleX(1)}}}}'
        f'@keyframes flow{{to{{transform:translateX(var(--period))}}}}'
        f'@keyframes fade{{from{{opacity:0}}to{{opacity:1}}}}'
        f'@keyframes pkt{{to{{stroke-dashoffset:-150}}}}'
        f'.bar{{transform-box:fill-box;transform-origin:left center;'
        f'animation:grow 1.5s cubic-bezier(.22,.9,.25,1) both}}'
        f'.flow{{animation:flow var(--dur) linear infinite}}'
        f'.fade{{animation:fade .8s ease-out both}}'
        f'.pkt{{animation:pkt 1.2s linear infinite}}'
        f'{extra_css}'
        f'@media (prefers-reduced-motion:reduce){{*{{animation:none!important}}}}'
        f'</style>'
        f'<g font-family="{SANS}">')


TAIL = '</g></svg>'


# ------------------------------------------------------------------ hero ---

HERO_W, HERO_H = 1280, 470
BAR_X, BAR_MAX = 64, 940
SLOW, FAST, CAP = 1.5, 8.5, 8.5


def hero(t):
    p = f'h{t["key"]}'
    slow_w = BAR_MAX * SLOW / CAP
    o = [head(HERO_W, HERO_H,
              'jtaccel — full-speed downloads for JetBrains Toolbox',
              'JetBrains Toolbox downloading over one connection reaches 1.5 MB/s; '
              'with jtaccel splitting the same download across 16 parallel streams it '
              'reaches 8.5 MB/s, about 5.7 times faster, with byte-identical output.',
              extra_css='.b2{animation-delay:.35s}')]
    o.append('<defs>'
             + cube_defs(p)
             + f'<radialGradient id="{p}glow" cx=".78" cy=".18" r=".62">'
               f'<stop offset="0" stop-color="{BRAND[1]}" stop-opacity="{t["glow"]}"/>'
               f'<stop offset="1" stop-color="{BRAND[1]}" stop-opacity="0"/>'
               f'</radialGradient>'
             + f'<linearGradient id="{p}fast" x1="0" y1="0" x2="1" y2="0">'
               f'<stop offset="0" stop-color="{BRAND[0]}"/>'
               f'<stop offset=".55" stop-color="{BRAND[1]}"/>'
               f'<stop offset="1" stop-color="{BRAND[2]}"/></linearGradient>'
             + '</defs>')

    o.append(f'<rect width="{HERO_W}" height="{HERO_H}" rx="18" fill="{t["bg"]}"/>')
    o.append(f'<rect width="{HERO_W}" height="{HERO_H}" rx="18" fill="url(#{p}glow)"/>')
    o.append(f'<rect x=".5" y=".5" width="{HERO_W - 1}" height="{HERO_H - 1}" rx="17.5" '
             f'fill="none" stroke="{t["border"]}"/>')

    # wordmark
    o.append(cube(64, 40, 74, p))
    o.append(f'<text x="164" y="106" font-size="56" font-weight="800" fill="{t["ink"]}" '
             f'letter-spacing="-1.8">jtaccel</text>')

    o.append(f'<text x="64" y="164" font-size="29" font-weight="650" fill="{t["ink"]}">'
             f'Full-speed downloads for JetBrains Toolbox</text>')
    o.append(f'<text x="64" y="197" font-size="16.5" fill="{t["muted"]}">'
             f'A local proxy that splits every large Toolbox download into 16 parallel '
             f'streams {esc("—")} and reassembles them byte-for-byte.</text>')

    # comparison rows
    def row(label, value, y, width, cls, fill, period, dur, bold):
        g = [f'<text x="{BAR_X}" y="{y}" font-size="13.5" font-weight="600" '
             f'letter-spacing=".7" fill="{t["muted"]}">{esc(label)}</text>',
             f'<rect x="{BAR_X}" y="{y + 12}" width="{BAR_MAX}" height="30" rx="15" '
             f'fill="{t["track"]}"/>',
             f'<rect class="bar {cls}" x="{BAR_X}" y="{y + 12}" width="{width:.1f}" '
             f'height="30" rx="15" fill="{fill}"/>',
             flow_stripes(BAR_X, y + 12, width, 30, period, dur, f'{p}{cls}c'),
             f'<text x="{HERO_W - 64}" y="{y + 34}" font-size="21" text-anchor="end" '
             f'font-weight="{700 if bold else 600}" '
             f'fill="{t["ink"] if bold else t["ink2"]}">{esc(value)}</text>']
        return ''.join(g)

    o.append(row('TOOLBOX AS IT SHIPS — ONE CONNECTION', '1.5 MB/s', 250,
                 slow_w, 'b1', BASELINE, 26, 3.6, False))
    o.append(row('WITH JTACCEL — 16 PARALLEL STREAMS', '8.5 MB/s', 330,
                 BAR_MAX, 'b2', f'url(#{p}fast)', 26, .62, True))

    # delta callout
    o.append(f'<g class="fade" style="animation-delay:1.2s">'
             f'<rect x="64" y="402" width="176" height="44" rx="12" fill="{ACCEL}" '
             f'fill-opacity=".14" stroke="{ACCEL}" stroke-opacity=".55"/>'
             f'<text x="152" y="431" font-size="20" font-weight="750" text-anchor="middle" '
             f'fill="{ACCEL}">{esc("≈ 5.7× faster")}</text>'
             f'<text x="262" y="431" font-size="15.5" fill="{t["muted"]}">'
             f'identical bytes {esc("—")} Toolbox{esc("’")}s own SHA-256 check still passes'
             f'</text></g>')
    o.append(TAIL)
    return ''.join(o)


# ---------------------------------------------------------- architecture ---

ARCH_W, ARCH_H = 1280, 560


def architecture(t):
    p = f'a{t["key"]}'
    o = [head(ARCH_W, ARCH_H,
              'How jtaccel accelerates a JetBrains Toolbox download',
              'Toolbox connects to the local jtaccel proxy. Authentication hosts are '
              'tunnelled blind and never decrypted. Any response of at least 32 MiB that '
              'advertises Accept-Ranges is re-fetched as 16 parallel ranged GETs of 1 MiB '
              'each and reassembled in order, so the bytes Toolbox receives are identical.')]
    o.append('<defs>' + cube_defs(p) + cube_defs(p + 't')
             + f'<marker id="{p}ar" viewBox="0 0 10 10" refX="8.5" refY="5" '
               f'markerWidth="7" markerHeight="7" orient="auto-start-reverse">'
               f'<path d="M0 0 L10 5 L0 10 z" fill="{t["muted"]}"/></marker>'
             + f'<marker id="{p}arA" viewBox="0 0 10 10" refX="8.5" refY="5" '
               f'markerWidth="7" markerHeight="7" orient="auto-start-reverse">'
               f'<path d="M0 0 L10 5 L0 10 z" fill="{ACCEL}"/></marker>'
             + '</defs>')
    o.append(f'<rect width="{ARCH_W}" height="{ARCH_H}" rx="18" fill="{t["bg"]}"/>')
    o.append(f'<rect x=".5" y=".5" width="{ARCH_W - 1}" height="{ARCH_H - 1}" rx="17.5" '
             f'fill="none" stroke="{t["border"]}"/>')

    o.append(f'<text x="48" y="56" font-size="20" font-weight="700" fill="{t["ink"]}">'
             f'How a Toolbox download actually gets faster</text>')
    o.append(f'<text x="48" y="82" font-size="14.5" fill="{t["muted"]}">'
             f'Acceleration is decided per response, so it works on any CDN {esc("—")} '
             f'there is no host allow-list to maintain.</text>')

    # --- node A: Toolbox. Labels sit above the cube so the return path can arrive
    # at its bottom vertex without crossing text.
    o.append(f'<text x="116" y="212" font-size="15" font-weight="650" text-anchor="middle" '
             f'fill="{t["ink"]}">JetBrains Toolbox</text>')
    o.append(f'<text x="116" y="231" font-size="13" text-anchor="middle" '
             f'fill="{t["muted"]}">unmodified</text>')
    o.append(cube(74, 244, 84, p + 't', 'toolbox'))

    # A -> B
    o.append(f'<path d="M170 292 L322 292" stroke="{t["muted"]}" stroke-width="1.6" '
             f'fill="none" marker-end="url(#{p}ar)"/>')
    o.append(f'<text x="246" y="282" font-size="12.5" text-anchor="middle" '
             f'fill="{t["muted"]}" font-family="{MONO}">CONNECT</text>')

    # --- node B: jtaccel
    bx, by, bw, bh = 332, 150, 316, 256
    o.append(f'<rect x="{bx}" y="{by}" width="{bw}" height="{bh}" rx="14" '
             f'fill="{t["panel"]}" stroke="{t["border"]}"/>')
    o.append(cube(bx + 20, by + 20, 26, p))
    o.append(f'<text x="{bx + 56}" y="{by + 41}" font-size="16" font-weight="750" '
             f'fill="{t["ink"]}">jtaccel</text>')
    o.append(f'<text x="{bx + bw - 18}" y="{by + 41}" font-size="12.5" text-anchor="end" '
             f'fill="{t["muted"]}" font-family="{MONO}">127.0.0.1:8899</text>')
    o.append(f'<line x1="{bx}" y1="{by + 58}" x2="{bx + bw}" y2="{by + 58}" '
             f'stroke="{t["border"]}"/>')

    rows = [
        (ACCEL, 'large + rangeable', 'split into 16 ranged GETs', True),
        (BASELINE, 'auth / OAuth hosts', 'blind TCP splice, never decrypted', False),
        (t['muted'], 'everything else', 'relayed verbatim', False),
    ]
    ry = by + 92
    for col, k, v, strong in rows:
        o.append(f'<circle cx="{bx + 26}" cy="{ry - 5}" r="4.5" fill="{col}"/>')
        o.append(f'<text x="{bx + 42}" y="{ry}" font-size="14" font-weight="650" '
                 f'fill="{t["ink"] if strong else t["ink2"]}">{esc(k)}</text>')
        o.append(f'<text x="{bx + 42}" y="{ry + 20}" font-size="12.5" '
                 f'fill="{t["muted"]}">{esc(v)}</text>')
        ry += 62

    # --- fan-out: 16 streams B -> C. A faint solid rail carries the shape; a short
    # dash travels along each one so the picture reads as flow rather than hatching.
    fx0, fx1 = bx + bw, 946
    n = 16
    for i in range(n):
        y0 = by + 34 + i * (bh - 68) / (n - 1)
        y1 = 212 + i * 132 / (n - 1)
        mid = (fx0 + fx1) / 2
        d = (f'M{fx0} {y0:.1f} C{mid:.0f} {y0:.1f} {mid:.0f} {y1:.1f} {fx1} {y1:.1f}')
        o.append(f'<path d="{d}" fill="none" stroke="{ACCEL}" stroke-opacity=".20" '
                 f'stroke-width="1.2"/>')
        # The dash pattern and its per-line phase are presentation attributes, so the
        # streams read as staggered packets even where the animation never runs; the
        # negative delay starts each line mid-cycle instead of waiting its turn.
        o.append(f'<path d="{d}" fill="none" stroke="{ACCEL}" stroke-opacity=".95" '
                 f'stroke-width="2.2" stroke-linecap="round" '
                 f'stroke-dasharray="22 128" stroke-dashoffset="{-i * 9.4:.1f}" '
                 f'class="pkt" style="animation-delay:{-i * .075:.3f}s"/>')
    o.append(f'<text x="{(fx0 + fx1) / 2:.0f}" y="150" font-size="13.5" '
             f'font-weight="650" text-anchor="middle" fill="{ACCEL}">'
             f'16 parallel ranged GETs</text>')
    o.append(f'<text x="{(fx0 + fx1) / 2:.0f}" y="170" font-size="12.5" '
             f'text-anchor="middle" fill="{t["muted"]}" font-family="{MONO}">'
             f'Range: bytes=… · 1 MiB segments</text>')

    # --- node C: origin
    cx, cy, cw, ch = 946, 196, 262, 164
    o.append(f'<rect x="{cx}" y="{cy}" width="{cw}" height="{ch}" rx="14" '
             f'fill="{t["panel"]}" stroke="{t["border"]}"/>')
    o.append(f'<text x="{cx + 20}" y="{cy + 34}" font-size="15" font-weight="700" '
             f'fill="{t["ink"]}">Origin CDN</text>')
    for i, s in enumerate(('download.jetbrains.com', 'edgedl.me.gvt1.com',
                           'plugins.jetbrains.com')):
        o.append(f'<text x="{cx + 20}" y="{cy + 60 + i * 21}" font-size="12.5" '
                 f'fill="{t["muted"]}" font-family="{MONO}">{esc(s)}</text>')
    o.append(f'<text x="{cx + 20}" y="{cy + 140}" font-size="12.5" font-weight="600" '
             f'fill="{BASELINE}" font-family="{MONO}">Accept-Ranges: bytes</text>')

    # --- return path: down out of jtaccel, along the bottom, up into the cube
    o.append(f'<path d="M{bx + bw / 2} {by + bh} L{bx + bw / 2} 478 L116 478 L116 356" '
             f'fill="none" stroke="{ACCEL}" stroke-width="2" '
             f'marker-end="url(#{p}arA)"/>')
    o.append(f'<rect x="196" y="456" width="330" height="44" rx="12" '
             f'fill="{t["panel2"]}" stroke="{t["border"]}"/>')
    o.append(f'<text x="361" y="484" font-size="14" font-weight="650" text-anchor="middle" '
             f'fill="{t["ink"]}">ordered reassembly {esc("→")} byte-exact stream</text>')
    o.append(f'<text x="558" y="484" font-size="13" fill="{t["muted"]}">'
             f'Toolbox{esc("’")}s own SHA-256 verification passes unchanged</text>')
    o.append(TAIL)
    return ''.join(o)


# ------------------------------------------------------------- benchmark ---

BM_W, BM_H = 1280, 426


def benchmark(t):
    p = f'b{t["key"]}'
    o = [head(BM_W, BM_H,
              'Measured throughput before and after jtaccel',
              'Panel one: Toolbox as it ships reaches 1.5 MB/s while jtaccel reaches '
              '8.5 MB/s on the same line. Panel two: on Android Studio’s CDN a single '
              'connection reaches 2.3 MB/s and six connections reach 7.8 MB/s against a '
              'line capacity of 8.5 MB/s. Panel three: time to first byte is 16 seconds '
              'with 8 MiB segments and 0.94 seconds with 1 MiB segments.')]
    o.append('<defs>'
             f'<linearGradient id="{p}f" x1="0" y1="0" x2="1" y2="0">'
             f'<stop offset="0" stop-color="{BRAND[0]}"/>'
             f'<stop offset=".55" stop-color="{BRAND[1]}"/>'
             f'<stop offset="1" stop-color="{BRAND[2]}"/></linearGradient></defs>')
    o.append(f'<rect width="{BM_W}" height="{BM_H}" rx="18" fill="{t["bg"]}"/>')
    o.append(f'<rect x=".5" y=".5" width="{BM_W - 1}" height="{BM_H - 1}" rx="17.5" '
             f'fill="none" stroke="{t["border"]}"/>')
    o.append(f'<text x="40" y="52" font-size="19" font-weight="700" fill="{t["ink"]}">'
             f'Measured on one 8.5 MB/s line, same file, same evening</text>')
    o.append(f'<text x="40" y="76" font-size="14" fill="{t["muted"]}">'
             f'Aggregate bandwidth was never the limit {esc("—")} a single TCP stream was.'
             f'</text>')

    panels = [
        dict(title='End-to-end Toolbox download', unit='MB/s · higher is better',
             series=[('Toolbox as it ships', 1.5, '1.5', False),
                     ('jtaccel, 16 streams', 8.5, '8.5', True)],
             scale=8.5, ref=None,
             note='≈ 5.7× on the same file, same line'),
        dict(title='Same CDN, more connections', unit='MB/s · higher is better',
             series=[('1 connection', 2.3, '2.3', False),
                     ('6 connections', 7.8, '7.8', False)],
             scale=8.5, ref=('line capacity', 8.5, '8.5'),
             note='the pipe was never the limit — one stream was'),
        dict(title='Time to first byte by segment', unit='seconds · lower is better',
             series=[('8 MiB segments', 16.0, '16.0 s', False),
                     ('1 MiB segments', 0.94, '0.94 s', True)],
             scale=16.0, ref=None,
             note='1 MiB is the shipped default'),
    ]

    px, pw, gap = 40, 386, 27
    for pi, pan in enumerate(panels):
        x = px + pi * (pw + gap)
        y, ph = 104, 286
        o.append(f'<rect x="{x}" y="{y}" width="{pw}" height="{ph}" rx="14" '
                 f'fill="{t["panel"]}" stroke="{t["border"]}"/>')
        o.append(f'<text x="{x + 22}" y="{y + 34}" font-size="15" font-weight="700" '
                 f'fill="{t["ink"]}">{esc(pan["title"])}</text>')
        o.append(f'<text x="{x + 22}" y="{y + 55}" font-size="12" '
                 f'fill="{t["muted"]}">{esc(pan["unit"])}</text>')

        inner = pw - 44
        by0 = y + 84
        rows = list(pan['series']) + ([pan['ref']] if pan['ref'] else [])
        for ri, row in enumerate(rows):
            is_ref = pan['ref'] is not None and ri == len(rows) - 1
            if is_ref:
                label, val, disp = row
                strong = False
            else:
                label, val, disp, strong = row
            ry = by0 + ri * 62
            o.append(f'<text x="{x + 22}" y="{ry}" font-size="13" '
                     f'fill="{t["muted"] if not strong else t["ink2"]}">{esc(label)}</text>')
            o.append(f'<text x="{x + pw - 22}" y="{ry}" font-size="15" text-anchor="end" '
                     f'font-weight="{700 if strong else 600}" '
                     f'fill="{t["ink"] if strong else t["ink2"]}">{esc(disp)}</text>')
            w = inner * val / pan['scale']
            o.append(f'<rect x="{x + 22}" y="{ry + 10}" width="{inner}" height="22" '
                     f'rx="11" fill="{t["track"]}"/>')
            if is_ref:
                o.append(f'<rect x="{x + 22.75}" y="{ry + 10.75}" width="{w - 1.5:.1f}" '
                         f'height="20.5" rx="10.25" fill="none" stroke="{t["muted"]}" '
                         f'stroke-width="1.5" stroke-dasharray="5 4"/>')
            else:
                fill = f'url(#{p}f)' if strong else BASELINE
                o.append(f'<rect class="bar" x="{x + 22}" y="{ry + 10}" width="{w:.1f}" '
                         f'height="22" rx="11" fill="{fill}" '
                         f'style="animation-delay:{.1 + ri * .12 + pi * .08:.2f}s"/>')
                if strong:
                    o.append(flow_stripes(x + 22, ry + 10, w, 22, 24, .7,
                                          f'{p}{pi}{ri}c', .15))
        o.append(f'<text x="{x + 22}" y="{y + ph - 22}" font-size="12" '
                 f'fill="{t["muted"]}">{esc(pan["note"])}</text>')
    o.append(TAIL)
    return ''.join(o)


# -------------------------------------------------------------- terminal ---

TERM_LINES = [
    ('p', '$ ', 'curl -fsSL https://github.com/App-Builders-Gang/'
                'jetbrains-toolbox-accelerator/releases/latest/download/install.sh | sh'),
    ('o', '', 'Downloading jtaccel-linux-amd64...'),
    ('g', '', 'Checksum OK.'),
    ('o', '', 'Installed jtaccel to /home/you/.local/bin/jtaccel'),
    ('o', '', 'Configuring Toolbox...'),
    ('d', '', 'Toolbox: /home/you/.local/share/JetBrains/Toolbox'),
    ('d', '', 'Truststore: /home/you/.config/jtaccel/truststore.p12'),
    ('d', '', 'Settings: proxy 127.0.0.1:8899 + keystore registered'),
    ('d', '', 'Autostart: registered'),
    ('g', '', 'Proxy: listening on 127.0.0.1:8899'),
    ('d', '', 'Toolbox: restarted'),
    ('s', '', ''),
    ('g', '', 'Done. Toolbox downloads are now accelerated.'),
    ('s', '', ''),
    ('p', '$ ', 'jtaccel status'),
    ('o', '', 'jtaccel 0.1.0 (linux/amd64)'),
    ('s', '', ''),
    ('d', '', '  proxy             127.0.0.1:8899  listening'),
    ('d', '', '  autostart         registered'),
    ('d', '', '  CA                valid, expires 2036-07-28 (3649 days)'),
    ('d', '', '  truststore        /home/you/.config/jtaccel/truststore.p12'),
    ('d', '', '  Toolbox           /home/you/.local/share/JetBrains/Toolbox (running)'),
    ('g', '', '  settings.proxy    http://127.0.0.1:8899  OK'),
    ('g', '', '  settings.keystore /home/you/.config/jtaccel/truststore.p12  OK'),
]

TERM_W = 1048


def terminal():
    lh, top = 22, 78
    h = top + len(TERM_LINES) * lh + 34
    o = [head(TERM_W, h, 'Installing jtaccel',
              'A terminal session: the install script downloads the release binary, '
              'verifies its SHA-256, installs it, configures Toolbox with a local proxy '
              'and trust store, and jtaccel status then reports every component healthy.',
              extra_css='.ln{animation:fade .28s ease-out both}')]
    o.append(f'<rect width="{TERM_W}" height="{h}" rx="14" fill="#0b0d12"/>')
    o.append(f'<rect x=".5" y=".5" width="{TERM_W - 1}" height="{h - 1}" rx="13.5" '
             f'fill="none" stroke="#232a35"/>')
    o.append(f'<path d="M0 14 a14 14 0 0 1 14 -14 h{TERM_W - 28} a14 14 0 0 1 14 14 '
             f'v30 h-{TERM_W} z" fill="#151a22"/>')
    for i, c in enumerate(('#ff5f57', '#febc2e', '#28c840')):
        o.append(f'<circle cx="{26 + i * 20}" cy="22" r="6" fill="{c}"/>')
    o.append(f'<text x="{TERM_W / 2}" y="27" font-size="12.5" text-anchor="middle" '
             f'fill="#6e7a8a" font-family="{MONO}">jtaccel {esc("—")} install</text>')

    col = {'p': '#e6edf3', 'o': '#c9d3de', 'g': '#3fd17c', 'd': '#8b96a5', 's': '#000'}
    for i, (kind, prompt, txt) in enumerate(TERM_LINES):
        if kind == 's':
            continue
        y = top + i * lh
        d = .12 + i * .13
        o.append(f'<g class="ln" style="animation-delay:{d:.2f}s">')
        x = 26
        if prompt:
            o.append(f'<text x="{x}" y="{y}" font-size="13.5" fill="{ACCEL}" '
                     f'font-weight="700" font-family="{MONO}">{esc(prompt)}</text>')
            x += 17
        weight = '600' if kind == 'p' else '400'
        # xml:space keeps the leading spaces that align `jtaccel status` into columns
        o.append(f'<text x="{x}" y="{y}" font-size="13.5" fill="{col[kind]}" '
                 f'font-weight="{weight}" font-family="{MONO}" xml:space="preserve">'
                 f'{esc(txt)}</text></g>')

    cy = top + (len(TERM_LINES) - 1) * lh + 14
    o.append(f'<rect x="26" y="{cy}" width="9" height="15" fill="{ACCEL}" '
             f'class="ln" style="animation-delay:{.12 + len(TERM_LINES) * .13:.2f}s">'
             f'<animate attributeName="opacity" values="1;1;0;0" dur="1.06s" '
             f'repeatCount="indefinite"/></rect>')
    o.append(TAIL)
    return ''.join(o)


# -------------------------------------------------------- social preview ---

SOC_W, SOC_H = 1280, 640


def social():
    """GitHub Open Graph card. Rendered to PNG by scripts/render-social.sh."""
    t = DARK
    p = 's'
    o = [head(SOC_W, SOC_H, 'jtaccel — full-speed downloads for JetBrains Toolbox',
              'Social preview card for the jetbrains-toolbox-accelerator project.')]
    o.append('<defs>' + cube_defs(p)
             + f'<radialGradient id="{p}g1" cx=".5" cy=".12" r=".78">'
               f'<stop offset="0" stop-color="{BRAND[1]}" stop-opacity=".30"/>'
               f'<stop offset="1" stop-color="{BRAND[1]}" stop-opacity="0"/>'
               f'</radialGradient>'
             + f'<linearGradient id="{p}fast" x1="0" y1="0" x2="1" y2="0">'
               f'<stop offset="0" stop-color="{BRAND[0]}"/>'
               f'<stop offset=".55" stop-color="{BRAND[1]}"/>'
               f'<stop offset="1" stop-color="{BRAND[2]}"/></linearGradient></defs>')
    o.append(f'<rect width="{SOC_W}" height="{SOC_H}" fill="{t["bg"]}"/>')
    o.append(f'<rect width="{SOC_W}" height="{SOC_H}" fill="url(#{p}g1)"/>')

    o.append(cube(SOC_W / 2 - 68, 74, 136, p))
    o.append(f'<text x="{SOC_W / 2}" y="316" font-size="86" font-weight="800" '
             f'text-anchor="middle" fill="{t["ink"]}" letter-spacing="-2.6">jtaccel</text>')
    o.append(f'<text x="{SOC_W / 2}" y="362" font-size="26" text-anchor="middle" '
             f'fill="{t["ink2"]}">Full-speed downloads for JetBrains Toolbox</text>')

    bx, bw = 300, 680
    for i, (lab, val, fill, bold) in enumerate(
            [('Toolbox, one connection', 1.5, BASELINE, False),
             ('jtaccel, 16 streams', 8.5, f'url(#{p}fast)', True)]):
        y = 424 + i * 62
        o.append(f'<text x="{bx}" y="{y}" font-size="15" text-anchor="end" '
                 f'fill="{t["muted"]}">{esc(lab)}</text>')
        o.append(f'<rect x="{bx + 20}" y="{y - 17}" width="{bw}" height="23" rx="11.5" '
                 f'fill="{t["track"]}"/>')
        w = bw * val / 8.5
        o.append(f'<rect x="{bx + 20}" y="{y - 17}" width="{w:.1f}" height="23" '
                 f'rx="11.5" fill="{fill}"/>')
        o.append(f'<text x="{bx + bw + 36}" y="{y}" font-size="19" '
                 f'font-weight="{700 if bold else 600}" '
                 f'fill="{t["ink"] if bold else t["ink2"]}">{val} MB/s</text>')

    o.append(f'<rect x="{SOC_W / 2 - 96}" y="534" width="192" height="44" rx="12" '
             f'fill="{ACCEL}" fill-opacity=".16" stroke="{ACCEL}" stroke-opacity=".6"/>')
    o.append(f'<text x="{SOC_W / 2}" y="563" font-size="21" font-weight="750" '
             f'text-anchor="middle" fill="{ACCEL}">{esc("≈ 5.7× faster")}</text>')
    o.append(f'<text x="{SOC_W / 2}" y="608" font-size="15" text-anchor="middle" '
             f'fill="{t["muted"]}" font-family="{MONO}">'
             f'github.com/App-Builders-Gang/jetbrains-toolbox-accelerator</text>')
    o.append(TAIL)
    return ''.join(o)


# ------------------------------------------------------------------ main ---

def write(name, content):
    path = os.path.normpath(os.path.join(OUT, name))
    # newline='' keeps LF on Windows too; .gitattributes normalises the repo to LF
    # and translating here would leave every regenerated file spuriously dirty.
    with open(path, 'w', encoding='utf-8', newline='') as f:
        f.write(content + '\n')
    print(f'{name:28s} {len(content) / 1024:6.1f} KiB')


def main():
    for t in (DARK, LIGHT):
        write(f'hero-{t["key"]}.svg', hero(t))
        write(f'architecture-{t["key"]}.svg', architecture(t))
        write(f'benchmark-{t["key"]}.svg', benchmark(t))
    write('terminal.svg', terminal())
    write('social-preview.svg', social())


if __name__ == '__main__':
    main()
