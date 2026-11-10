"""Draws WarpGo's icons (the Android launcher's shield on an orange tile).

python tools/make_icons.py  ->  winres/icon.ico, internal/ui/tray-{on,off}.ico, docs/icon-*.png
"""
from pathlib import Path
from PIL import Image, ImageDraw

ROOT = Path(__file__).resolve().parent.parent
BIG = 1024


def bezier(p0, p1, p2, p3, n=40):
    out = []
    for i in range(n + 1):
        t = i / n
        a = (1 - t) ** 3; b = 3 * (1 - t) ** 2 * t; c = 3 * (1 - t) * t ** 2; d = t ** 3
        out.append((a * p0[0] + b * p1[0] + c * p2[0] + d * p3[0], a * p0[1] + b * p1[1] + c * p2[1] + d * p3[1]))
    return out


def shield(s):
    k = s / 108
    pts = [(54, 24), (77, 34), (77, 54)]
    pts += bezier((77, 54), (77, 69), (67, 79), (54, 84.5))[1:]
    pts += bezier((54, 84.5), (41, 79), (31, 69), (31, 54))[1:]
    pts += [(31, 34)]
    return [(x * k, y * k) for x, y in pts]


def tile(color, dot):
    im = Image.new("RGBA", (BIG, BIG), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    d.rounded_rectangle([0, 0, BIG - 1, BIG - 1], radius=int(BIG * 0.22), fill=color)
    d.polygon(shield(BIG), fill="white")
    r = 8.5 * BIG / 108
    cx, cy = 54 * BIG / 108, 45 * BIG / 108
    d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=dot)
    return im


SIZES = [16, 20, 24, 32, 40, 48, 64, 128, 256]
on = tile("#F6821F", "#F6821F")
off = tile("#7d776f", "#7d776f")
on.save(ROOT / "winres" / "icon.ico", sizes=[(s, s) for s in SIZES])
on.save(ROOT / "internal" / "ui" / "tray-on.ico", sizes=[(s, s) for s in (16, 20, 24, 32, 48)])
off.save(ROOT / "internal" / "ui" / "tray-off.ico", sizes=[(s, s) for s in (16, 20, 24, 32, 48)])
for s in (256, 512):
    on.resize((s, s), Image.LANCZOS).save(ROOT / "docs" / f"icon-{s}.png")
print("icons written")
