"""Render demo.html frame by frame and encode docs/rterm-demo.gif.

Usage: python3 docs/animation/record.py   (needs playwright, Chromium and ffmpeg)
"""
import pathlib
import subprocess
import tempfile

from playwright.sync_api import sync_playwright

HERE = pathlib.Path(__file__).resolve().parent
OUT = HERE.parent / "rterm-demo.gif"
FPS = 12
DURATION = 19.5  # seconds; the last few hold on the final state before looping

with tempfile.TemporaryDirectory() as tmp:
    with sync_playwright() as p:
        browser = p.chromium.launch()
        page = browser.new_page(viewport={"width": 960, "height": 600}, device_scale_factor=1)
        page.goto((HERE / "demo.html").as_uri())
        page.wait_for_timeout(300)  # fonts
        n = int(DURATION * FPS)
        for i in range(n):
            page.evaluate(f"render({i / FPS})")
            page.screenshot(path=f"{tmp}/f{i:04d}.png")
        browser.close()
    subprocess.run(
        [
            "ffmpeg", "-y", "-loglevel", "error", "-framerate", str(FPS), "-i", f"{tmp}/f%04d.png",
            "-vf", "split[a][b];[a]palettegen=max_colors=96:stats_mode=diff[p];"
                   "[b][p]paletteuse=dither=none:diff_mode=rectangle",
            "-loop", "0", str(OUT),
        ],
        check=True,
    )
print(f"wrote {OUT} ({OUT.stat().st_size // 1024} KB)")
