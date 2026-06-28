# Intro & Tutorial Videos — Design

| | |
|---|---|
| **Document** | Design Specification |
| **Project** | outbox-md |
| **Status** | Approved — building |
| **Date** | 2026-06-28 |

## Goal

Produce narrated, slide-based videos that explain **what outbox-md is** and **how to use it**, hosted on YouTube and linked from the README hero, so newcomers understand the project and how to contribute.

## Deliverables

1. **Explainer** — "What is outbox-md?" A podcast-style Q&A: **Andrew** asks, **Ava** answers. Covers the broken feedback loop on AI-generated specs, the outbox model, agent-agnostic MCP, and how to contribute. ~2–3 min.
2. **Tutorial** — "Using outbox-md (v1)". Walks the real quickstart: `docker run` → open browser → comment on text → connect an agent via MCP → accept → file rewritten and re-anchored. ~2–3 min.
3. **Silent GIF hero** — a short (~6 s) loop for inline README autoplay.
4. **Thumbnails** — title-slide PNGs (with a play badge) that link to the YouTube videos.

## Production

- **Voices:** edge-tts (free, no API key). Andrew = `en-US-AndrewMultilingualNeural`, Ava = `en-US-AvaMultilingualNeural`.
- **Slides:** rendered programmatically with Pillow at 1920×1080 — dark GitHub-style theme, title / bullets / simple diagram layouts. No browser dependency, fully reproducible.
- **Assembly:** ffmpeg. One still-image video segment per dialogue line (slide image + that line's audio), concatenated; subtitles (`Speaker: text`) burned in from the per-line durations.

## Pipeline

`scripts/build_video.py <script.json> <out-dir>`:
1. Render each line to audio via edge-tts (voice per speaker).
2. Probe each clip's duration with ffprobe.
3. Render each referenced slide to PNG (Pillow).
4. Build a per-line segment with ffmpeg; build an SRT from cumulative durations.
5. Concatenate segments; burn subtitles → `final.mp4`.
6. Emit `thumbnail.png` (title slide + play badge) and, for the explainer, `hero.gif`.

## Script format (`docs/media/*.script.json`)

```json
{
  "title": "What is outbox-md?",
  "slides": [
    { "id": 0, "kind": "title", "title": "outbox-md", "subtitle": "..." },
    { "id": 1, "kind": "bullets", "title": "...", "bullets": ["...", "..."] },
    { "id": 2, "kind": "diagram", "title": "The loop", "nodes": ["Reviewer", "Outbox", "Agent"] }
  ],
  "lines": [
    { "speaker": "Andrew", "text": "...", "slide": 0 },
    { "speaker": "Ava", "text": "...", "slide": 1 }
  ]
}
```

## Repo policy

- **Committed:** the pipeline (`scripts/build_video.py`), the script JSONs, the rendered **thumbnails** and **hero GIF** (small, referenced by the README).
- **Not committed:** the `.mp4` outputs (large binaries) — generated locally, uploaded to YouTube. `.gitignore` excludes `docs/media/out/`.
- **README:** a hero block (GIF + thumbnail linking to YouTube) and a "Watch / Learn" section. YouTube URLs are clearly-marked placeholders until upload.

## Non-goals

Live screen-capture of the running UI (slide-based tutorial is enough for v1); premium/cloud TTS; motion graphics/animation.
