# Toss

Throw files to convert them, on Windows.

Drag a file in Explorer or on the Desktop and swing it fast. Bins show up around the edges of the screen, one per format. Let go mid-swing and the file flies off, bounces off the screen edges and lands in a bin. The converted copy appears next to the original.

- **Images** (PNG, JPG, GIF, BMP, TIFF, WebP) → PNG, JPG, WebP, or PDF. Several images thrown together become one PDF.
- **Video** → MP4, GIF, MP3, and **audio** → MP3, WAV, if [ffmpeg](https://ffmpeg.org) is on your PATH (`winget install ffmpeg`).

Originals are never touched and existing files are never overwritten (you get `photo (2).jpg`). A miss does nothing. If you slow down, the bins get out of the way so normal drag and drop still works. Escape cancels.

It lives in the tray, with Pause, Start with Windows, and Quit. There's no window.

## Install

Grab `Toss-Setup.exe` from Releases (or from the `Toss-Setup` artifact of a CI run). It installs for your user only, so no admin prompt. Uninstall it from Settings → Apps.

## Build

Go 1.26, no cgo:

```
GOOS=windows go build -ldflags "-H windowsgui" -o toss.exe .
go test ./...
```

The physics, conversion and drawing code is platform-neutral and tested on Linux too. The installer is built by `installer/toss.iss` in CI.

GPL-3.0. Forked from [Open Tangerine](https://github.com/sintaxerrorpvtltd/Open-Tangerine) and rewritten from scratch.
