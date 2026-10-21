# Continuity

Plays a playlist of videos on a 24/7 loop to an RTMP server, with a web page for uploading, reordering and deleting videos.

## Requirements

- Go
- ffmpeg and ffprobe on the `PATH`

## Running

```sh
go run . -rtmp rtmp://example.com/live/streamkey
```

Then open http://localhost:8080.

| Flag       | Default   | Purpose                                  |
|------------|-----------|------------------------------------------|
| `-rtmp`    |           | RTMP URL to stream to (required)         |
| `-media`   | `media`   | Directory videos are stored in           |
| `-addr`    | `:8080`   | Address for the web interface            |
| `-encoder` | `libx264` | ffmpeg H.264 encoder used for the output |
| `-title`   | `Continuity` | Title shown on the web interface      |

To build a single binary instead: `go build -o continuity .`

## Raspberry Pi 4

Build on another machine for 64-bit Raspberry Pi OS (use `GOARCH=arm GOARM=7` for 32-bit):

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o continuity-pi .
```

Copy `continuity-pi` to the Pi, install ffmpeg with `sudo apt install ffmpeg`, and run it with the Pi's hardware encoder, since the CPU is likely too slow for 1080p30 x264:

```sh
./continuity-pi -rtmp rtmp://example.com/live/streamkey -encoder h264_v4l2m2m
```

## Videos

- Uploads must be MP4 files with H.264 video and AAC audio. Anything else, or a file that is cut short, is rejected. Uploads are also rejected if they would leave less than 1 GB free on the disk.
- Output is always 1920x1080 at 30fps (letterboxed as needed), 4.5 Mbps H.264 and 128 kbps AAC.
- The play order is stored in `media/playlist.json`, a plain list of filenames.
- Reorders and deletes take effect when the current clip finishes.
- With an empty playlist the stream shows black until a video is uploaded. A video that can't be played is replaced by a second of black.
- If the RTMP connection drops, or the encoder outputs nothing for 15 seconds (for example because the RTMP server stopped responding), the encoder restarts after 5 seconds.
- `media/playlist.json` is replaced atomically on every change, so a power cut can't leave it half written.
- The web page shows a live preview of the output, a few seconds behind. The preview's HLS files are kept in a temporary directory (removed on shutdown), and if the preview fails the RTMP stream carries on. The preview writes about 50 GB a day, so on a Raspberry Pi point `TMPDIR` at a RAM disk such as `/dev/shm` to spare the SD card.

## How it works

One long-running ffmpeg encodes MPEG-TS from its stdin and sends it to both the RTMP server and the HLS preview (using ffmpeg's tee muxer, so it's encoded once). For each clip in turn, a short-lived ffmpeg remuxes it into that pipe without re-encoding (`-c copy -f mpegts`), offsetting its timestamps by the total length played so far so the stream stays continuous. Uploads are checked with ffprobe.
