"""Turn build-resources/icon-source.webp into a macOS-ready 1024px icon.

The artwork is a rounded square on solid black. Outside the rounded square
the black becomes transparent (the glow is kept as soft alpha), and the
square is scaled to Apple's icon grid: an 824px body centred in 1024px.
"""
import sys
from pathlib import Path
from PIL import Image, ImageDraw, ImageFilter

root = Path(__file__).resolve().parent.parent
src = Image.open(root / 'build-resources' / 'icon-source.webp').convert('RGB')
w, h = src.size
px = src.load()

def bright(p):
    return max(p)

# Find the bright outline of the rounded square on the middle row / column.
mid_y, mid_x = h // 2, w // 2
row = [bright(px[x, mid_y]) for x in range(w)]
col = [bright(px[mid_x, y]) for y in range(h)]
peak = max(row)
thr = peak * 0.6
left = next(x for x in range(w) if row[x] >= thr)
right = next(x for x in range(w - 1, -1, -1) if row[x] >= thr)
top = next(y for y in range(h) if col[y] >= thr)
bottom = next(y for y in range(h - 1, -1, -1) if col[y] >= thr)
body = max(right - left, bottom - top)
radius = round(body * 0.225)

# Alpha: opaque inside the rounded square, glow brightness outside it.
inside = Image.new('L', (w, h), 0)
ImageDraw.Draw(inside).rounded_rectangle([left, top, right, bottom], radius=radius, fill=255)
inside = inside.filter(ImageFilter.GaussianBlur(1))
# The glow was drawn over black: un-blend it so it's pure light with
# transparency (no dark fringe on light backgrounds).
glow_rgba = Image.new('RGBA', (w, h))
gp = glow_rgba.load()
for y in range(h):
    for x in range(w):
        r, g, b = px[x, y]
        m = max(r, g, b)
        if m == 0:
            gp[x, y] = (0, 0, 0, 0)
        else:
            gp[x, y] = (r * 255 // m, g * 255 // m, b * 255 // m, m)
solid = src.convert('RGBA')
rgba = Image.composite(solid, glow_rgba, inside)

# Scale so the body is 824px and centre it on a 1024px canvas.
scale = 824 / body
size = round(w * scale)
scaled = rgba.resize((size, size), Image.LANCZOS)
canvas = Image.new('RGBA', (1024, 1024), (0, 0, 0, 0))
cx = round((left + right) / 2 * scale)
cy = round((top + bottom) / 2 * scale)
canvas.paste(scaled, (512 - cx, 512 - cy), scaled)
out = root / 'build-resources' / 'icon.png'
canvas.save(out)
print(f'icon: body {body}px at ({left},{top})-({right},{bottom}) -> {out}', file=sys.stderr)
