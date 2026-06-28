# Background music

The videos use **"Coffee & Streets" by [Aylex](https://aylex.net)** — free / no-copyright music
(free for any project, including monetized; attribution appreciated).

The track file is **not committed** (it's licensed for use *in* content, not for redistribution
as a standalone file). Fetch it before rendering with music:

```bash
yt-dlp -x --audio-format mp3 \
  -o "docs/media/bgm/coffee-and-streets.%(ext)s" \
  "https://www.youtube.com/watch?v=q9QgRhn6-VY"
```

`scripts/build_video.py` mixes it at **16% volume** with a 2 s fade-in and 3 s fade-out, looping
to cover the full video. If the file is missing, the pipeline renders narration without music.
