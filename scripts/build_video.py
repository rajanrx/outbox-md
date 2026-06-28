#!/usr/bin/env python3
"""Build a narrated, slide-based video from a script JSON.

Usage:
    python3 scripts/build_video.py docs/media/explainer.script.json docs/media/out/explainer

Pipeline: edge-tts (Andrew/Ava voices) -> per-line audio; Pillow -> slide PNGs;
ffmpeg -> per-line segments + concat + burned subtitles -> final.mp4, plus a
thumbnail.png and (optionally) hero.gif.

No API keys. edge-tts needs internet; ffmpeg/ffprobe and Pillow must be installed.
"""
import json
import os
import subprocess
import sys
import textwrap

from PIL import Image, ImageDraw, ImageFont

W, H = 1920, 1080
BG = (13, 17, 23)        # GitHub dark
PANEL = (22, 27, 34)
FG = (230, 237, 243)
MUTED = (139, 148, 158)
ACCENT = (45, 212, 191)  # teal
BLUE = (47, 129, 247)

VOICES = {
    "Andrew": "en-US-AndrewMultilingualNeural",
    "Ava": "en-US-AvaMultilingualNeural",
}
SPEAKER_COLOR = {"Andrew": BLUE, "Ava": ACCENT}

FONT_BOLD = "/System/Library/Fonts/Supplemental/Arial Bold.ttf"
FONT_REG_CANDIDATES = [
    "/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
    "/System/Library/Fonts/SFNS.ttf",
    "/System/Library/Fonts/Helvetica.ttc",
]


def font(size, bold=False):
    path = FONT_BOLD if bold else next((p for p in FONT_REG_CANDIDATES if os.path.exists(p)), None)
    if path and os.path.exists(path):
        try:
            return ImageFont.truetype(path, size)
        except Exception:
            pass
    return ImageFont.load_default()


def run(cmd):
    subprocess.run(cmd, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def wrap(draw, text, fnt, max_w):
    out = []
    for para in text.split("\n"):
        words, line = para.split(" "), ""
        for w in words:
            trial = (line + " " + w).strip()
            if draw.textlength(trial, font=fnt) <= max_w:
                line = trial
            else:
                if line:
                    out.append(line)
                line = w
        out.append(line)
    return out


def rounded(draw, box, radius, fill=None, outline=None, width=1):
    draw.rounded_rectangle(box, radius=radius, fill=fill, outline=outline, width=width)


def render_slide(slide, path):
    render_slide_img(slide).save(path)


def render_slide_img(slide):
    img = Image.new("RGB", (W, H), BG)
    d = ImageDraw.Draw(img)
    # top accent bar + brand
    d.rectangle([0, 0, W, 8], fill=ACCENT)
    d.text((80, 60), "outbox-md", font=font(34, True), fill=MUTED)
    kind = slide.get("kind", "bullets")

    if kind == "title":
        t = font(120, True)
        lines = wrap(d, slide["title"], t, W - 320)
        y = H // 2 - (len(lines) * 130) // 2 - 60
        for ln in lines:
            d.text((160, y), ln, font=t, fill=FG)
            y += 130
        if slide.get("subtitle"):
            st = font(48)
            for ln in wrap(d, slide["subtitle"], st, W - 360):
                d.text((160, y + 20), ln, font=st, fill=MUTED)
                y += 64
    elif kind == "diagram":
        d.text((160, 150), slide["title"], font=font(72, True), fill=FG)
        nodes = slide.get("nodes", [])
        n = len(nodes)
        gap = 80
        bw = (W - 320 - gap * (n - 1)) // max(n, 1)
        bw = min(bw, 460)
        total = bw * n + gap * (n - 1)
        x = (W - total) // 2
        cy = H // 2 + 40
        bh = 180
        for i, label in enumerate(nodes):
            box = [x, cy - bh // 2, x + bw, cy + bh // 2]
            rounded(d, box, 28, fill=PANEL, outline=ACCENT, width=4)
            lf = font(44, True)
            tw = d.textlength(label, font=lf)
            d.text((x + (bw - tw) // 2, cy - 26), label, font=lf, fill=FG)
            if i < n - 1:
                ax0, ax1 = x + bw + 12, x + bw + gap - 12
                d.line([ax0, cy, ax1, cy], fill=BLUE, width=6)
                d.polygon([(ax1, cy), (ax1 - 18, cy - 12), (ax1 - 18, cy + 12)], fill=BLUE)
            x += bw + gap
        if slide.get("caption"):
            cf = font(40)
            cap = slide["caption"]
            tw = d.textlength(cap, font=cf)
            d.text(((W - tw) // 2, cy + bh // 2 + 70), cap, font=cf, fill=MUTED)
    else:  # bullets
        d.text((160, 150), slide["title"], font=font(76, True), fill=FG)
        bf = font(50)
        y = 340
        for b in slide.get("bullets", []):
            d.ellipse([170, y + 22, 194, y + 46], fill=ACCENT)
            for j, ln in enumerate(wrap(d, b, bf, W - 420)):
                d.text((240, y), ln, font=bf, fill=FG)
                y += 70
            y += 28

    return img


def render_frame(slide, speaker, text, path):
    """Render a slide with a caption bar (speaker + spoken line) at the bottom."""
    img = render_slide_img(slide)
    d = ImageDraw.Draw(img)
    panel_top = 824
    d.rectangle([0, panel_top, W, H], fill=(8, 11, 15))
    d.rectangle([0, panel_top, W, panel_top + 4], fill=ACCENT)
    nf = font(32, True)
    tf = font(32)
    col = SPEAKER_COLOR.get(speaker, FG)
    d.text((80, panel_top + 20), f"{speaker}", font=nf, fill=col)
    lines = wrap(d, text, tf, W - 160)[:4]
    y = panel_top + 66
    for ln in lines:
        d.text((80, y), ln, font=tf, fill=FG)
        y += 44
    img.save(path)


def add_play_badge(src, dst):
    img = Image.open(src).convert("RGB")
    d = ImageDraw.Draw(img)
    cx, cy, r = W // 2, H // 2 + 40, 110
    d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=(0, 0, 0), outline=FG, width=8)
    d.polygon([(cx - 35, cy - 55), (cx - 35, cy + 55), (cx + 65, cy)], fill=FG)
    img.save(dst)


def srt_ts(t):
    h = int(t // 3600)
    m = int((t % 3600) // 60)
    s = int(t % 60)
    ms = int((t - int(t)) * 1000)
    return f"{h:02d}:{m:02d}:{s:02d},{ms:03d}"


def duration(path):
    out = subprocess.run(
        ["ffprobe", "-v", "error", "-show_entries", "format=duration",
         "-of", "default=noprint_wrappers=1:nokey=1", path],
        check=True, capture_output=True, text=True).stdout.strip()
    return float(out)


def main():
    if len(sys.argv) != 3:
        print(__doc__)
        sys.exit(1)
    script_path, out_dir = sys.argv[1], sys.argv[2]
    script = json.load(open(script_path))
    os.makedirs(out_dir, exist_ok=True)
    work = os.path.join(out_dir, "work")
    os.makedirs(work, exist_ok=True)

    slides = {s["id"]: s for s in script["slides"]}
    lines = script["lines"]
    segs = []
    for i, ln in enumerate(lines):
        voice = VOICES[ln["speaker"]]
        audio = os.path.join(work, f"line_{i}.mp3")
        if not os.path.exists(audio):
            run(["edge-tts", "--voice", voice, "--text", ln["text"], "--write-media", audio])
        dur = duration(audio) + 0.35  # small tail
        frame = os.path.join(work, f"frame_{i}.png")
        render_frame(slides[ln["slide"]], ln["speaker"], ln["text"], frame)
        seg = os.path.join(work, f"seg_{i}.mp4")
        run([
            "ffmpeg", "-y", "-loop", "1", "-i", frame, "-i", audio,
            "-c:v", "libx264", "-tune", "stillimage", "-pix_fmt", "yuv420p",
            "-r", "25", "-t", f"{dur:.3f}", "-c:a", "aac", "-b:a", "192k",
            "-af", "apad=pad_dur=0.35", seg,
        ])
        segs.append(seg)
        print(f"  line {i+1}/{len(lines)} ({ln['speaker']}, {dur:.1f}s)")

    listfile = os.path.join(work, "list.txt")
    with open(listfile, "w") as f:
        for s in segs:
            f.write(f"file '{os.path.abspath(s)}'\n")
    final = os.path.join(out_dir, "final.mp4")
    run(["ffmpeg", "-y", "-f", "concat", "-safe", "0", "-i", listfile, "-c", "copy", final])
    print(f"wrote {final} ({duration(final):.1f}s)")

    thumb = os.path.join(out_dir, "thumbnail.png")
    slide0 = os.path.join(work, "slide_0.png")
    render_slide(slides[0], slide0)
    add_play_badge(slide0, thumb)
    print(f"wrote {thumb}")

    if script.get("gif"):
        gif = os.path.join(out_dir, "hero.gif")
        palette = os.path.join(work, "palette.png")
        run(["ffmpeg", "-y", "-t", "6", "-i", final, "-vf",
             "fps=10,scale=720:-1:flags=lanczos,palettegen", palette])
        run(["ffmpeg", "-y", "-t", "6", "-i", final, "-i", palette, "-lavfi",
             "fps=10,scale=720:-1:flags=lanczos[x];[x][1:v]paletteuse", gif])
        print(f"wrote {gif}")


if __name__ == "__main__":
    main()
