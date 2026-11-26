"""Generate Windows 11 squircle .ico + Android ColorOS 17 Adaptive Icons from a 1024x1024 master PNG."""
import math
import os
import sys
from PIL import Image, ImageDraw, ImageFilter

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
MASTER = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "assets", "icon_master.png")
RES_DIR = os.path.join(ROOT, "android", "app", "src", "main", "res")
WIN_ICO = os.path.join(ROOT, "windows", "runner", "resources", "app_icon.ico")
UI_ICON = os.path.join(ROOT, "assets", "app_icon_squircle.png")


def make_squircle_mask(size: int, n: float = 4.6, margin_ratio: float = 0.04) -> Image.Image:
    """Continuous-curve Apple/ColorOS 17/Win11 superellipse (squircle) mask with 4x supersampling."""
    ss = 4
    s = size * ss
    mask = Image.new("L", (s, s), 0)
    cx = cy = (s - 1) / 2.0
    r = (s / 2.0) * (1.0 - 2.0 * margin_ratio)
    px = mask.load()
    inv_r = 1.0 / r
    for y in range(s):
        dy = abs(y - cy) * inv_r
        if dy > 1.05:
            continue
        dyn = dy ** n
        for x in range(s):
            dx = abs(x - cx) * inv_r
            if dx > 1.05:
                continue
            val = (dx ** n) + dyn
            if val <= 1.0:
                px[x, y] = 255
    return mask.resize((size, size), Image.Resampling.LANCZOS)


def apply_squircle(img: Image.Image, size: int, margin_ratio: float = 0.04) -> Image.Image:
    base = img.resize((size, size), Image.Resampling.LANCZOS).convert("RGBA")
    mask = make_squircle_mask(size, n=4.6, margin_ratio=margin_ratio)
    out = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    out.paste(base, (0, 0), mask)
    return out


def main() -> None:
    if not os.path.exists(MASTER):
        raise SystemExit(f"Master icon not found: {MASTER}")
    master = Image.open(MASTER).convert("RGBA")
    w, h = master.size
    if w != h:
        side = min(w, h)
        left = (w - side) // 2
        top = (h - side) // 2
        master = master.crop((left, top, left + side, top + side))

    os.makedirs(os.path.dirname(UI_ICON), exist_ok=True)
    # 1. In-app high-res squircle PNG (512x512)
    squircle_512 = apply_squircle(master, 512, margin_ratio=0.02)
    squircle_512.save(UI_ICON, "PNG")

    # 2. Windows 11 multi-resolution .ico (16..256px) with subtle 4% Win11 Fluent margin
    win_sizes = [(256, 256), (128, 128), (64, 64), (48, 48), (32, 32), (24, 24), (16, 16)]
    win_256 = apply_squircle(master, 256, margin_ratio=0.04)
    os.makedirs(os.path.dirname(WIN_ICO), exist_ok=True)
    win_256.save(WIN_ICO, format="ICO", sizes=win_sizes)

    # 3. Android Legacy & Adaptive Icon mipmaps (ColorOS 17 compliant)
    # Adaptive icon canvas is 108dp; visible mask is 72dp (66.7%), safe zone 66dp (61.1%).
    # To guarantee zero clipping on ANY ColorOS 17 shape (squircle, pebble, circle), we put the
    # full-bleed edge-to-edge background in ic_launcher_background and scale master slightly
    # into ic_launcher_foreground centered with safe bleed.
    legacy_densities = {
        "mipmap-mdpi": 48,
        "mipmap-hdpi": 72,
        "mipmap-xhdpi": 96,
        "mipmap-xxhdpi": 144,
        "mipmap-xxxhdpi": 192,
    }
    adaptive_densities = {
        "mipmap-mdpi": 108,
        "mipmap-hdpi": 162,
        "mipmap-xhdpi": 216,
        "mipmap-xxhdpi": 324,
        "mipmap-xxxhdpi": 432,
    }

    # Save clean lossless master PNG in repo assets
    master.resize((1024, 1024), Image.Resampling.LANCZOS).save(os.path.join(ROOT, "assets", "icon_master.png"), "PNG")

    # Build smooth blurred full-bleed background from master so ColorOS 17 zoom/parallax animations never show seams
    bg_master = master.resize((512, 512), Image.Resampling.LANCZOS).filter(ImageFilter.GaussianBlur(radius=42))

    # Build soft-feathered alpha mask so the scaled artwork blends seamlessly into the adaptive background
    def feathered_center_layer(size: int, inner_ratio: float = 0.72) -> Image.Image:
        inner = int(size * inner_ratio)
        scaled = master.resize((inner, inner), Image.Resampling.LANCZOS).convert("RGBA")
        # Soft feather along the outer 12% of the inner square so gradient blends seamlessly
        feather = Image.new("L", (inner, inner), 0)
        pad = max(4, int(inner * 0.12))
        draw = ImageDraw.Draw(feather)
        draw.rounded_rectangle((pad, pad, inner - pad, inner - pad), radius=int(inner * 0.22), fill=255)
        feather = feather.filter(ImageFilter.GaussianBlur(radius=pad * 0.75))
        scaled.putalpha(feather)
        canvas = Image.new("RGBA", (size, size), (0, 0, 0, 0))
        offset = (size - inner) // 2
        canvas.paste(scaled, (offset, offset), scaled)
        return canvas

    for folder, size in legacy_densities.items():
        d = os.path.join(RES_DIR, folder)
        os.makedirs(d, exist_ok=True)
        # Legacy launcher icon (ColorOS squircle)
        apply_squircle(master, size, margin_ratio=0.04).save(os.path.join(d, "ic_launcher.png"), "PNG")

    for folder, size in adaptive_densities.items():
        d = os.path.join(RES_DIR, folder)
        os.makedirs(d, exist_ok=True)
        bg_master.resize((size, size), Image.Resampling.LANCZOS).save(
            os.path.join(d, "ic_launcher_background.png"), "PNG"
        )
        feathered_center_layer(size, inner_ratio=0.72).save(
            os.path.join(d, "ic_launcher_foreground.png"), "PNG"
        )

    print("Generated Windows ICO, in-app squircle PNG, and Android ColorOS 17 Adaptive Icons successfully!")


if __name__ == "__main__":
    main()
