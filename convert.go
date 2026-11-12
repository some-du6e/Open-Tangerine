package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/HugoSmits86/nativewebp"
	"github.com/go-pdf/fpdf"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

var (
	imageExts = set(".png", ".jpg", ".jpeg", ".gif", ".bmp", ".tif", ".tiff", ".webp")
	videoExts = set(".mp4", ".mov", ".mkv", ".webm", ".avi", ".m4v")
	audioExts = set(".mp3", ".wav", ".flac", ".ogg", ".m4a", ".aac", ".opus")
)

const maxPixels = 100_000_000

func set(items ...string) map[string]bool {
	m := map[string]bool{}
	for _, s := range items {
		m[s] = true
	}
	return m
}

// targets lists what every one of paths can be turned into. Media targets need ffmpeg.
func targets(paths []string, ffmpeg bool) []string {
	if len(paths) == 0 || len(paths) > 100 {
		return nil
	}
	exts := map[string]bool{}
	for _, p := range paths {
		exts[strings.ToLower(filepath.Ext(p))] = true
	}
	within := func(allowed map[string]bool) bool {
		for e := range exts {
			if !allowed[e] {
				return false
			}
		}
		return true
	}
	var out []string
	switch {
	case within(imageExts):
		out = []string{"PNG", "JPG", "WEBP", "PDF"}
	case within(videoExts) && ffmpeg:
		out = []string{"MP4", "GIF", "MP3"}
	case within(audioExts) && ffmpeg:
		out = []string{"MP3", "WAV"}
	}
	// Converting PNGs to PNG is pointless; leave out the format they already are.
	if len(exts) == 1 {
		for e := range exts {
			same := strings.TrimPrefix(e, ".")
			if same == "jpeg" {
				same = "jpg"
			}
			out = without(out, strings.ToUpper(same))
		}
	}
	return out
}

func without(list []string, drop string) []string {
	var out []string
	for _, s := range list {
		if s != drop {
			out = append(out, s)
		}
	}
	return out
}

// convert writes new files beside the originals and returns their paths.
// Originals are never touched and existing files are never replaced.
func convert(paths []string, target string) ([]string, error) {
	if target == "PDF" {
		out, err := imagesToPDF(paths)
		if err != nil {
			return nil, err
		}
		return []string{out}, nil
	}
	var outs []string
	var errs []error
	for _, p := range paths {
		var out string
		var err error
		if imageExts[strings.ToLower(filepath.Ext(p))] {
			out, err = convertImage(p, target)
		} else {
			out, err = convertMedia(p, target)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(p), err))
			continue
		}
		outs = append(outs, out)
	}
	return outs, errors.Join(errs...)
}

func loadImage(path string) (image.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("not a readable image: %w", err)
	}
	if cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("image is too large (%dx%d)", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

func convertImage(path, target string) (string, error) {
	img, err := loadImage(path)
	if err != nil {
		return "", err
	}
	return writeNew(path, strings.ToLower(target), func(f *os.File) error {
		switch target {
		case "PNG":
			return png.Encode(f, img)
		case "JPG":
			return jpeg.Encode(f, flatten(img), &jpeg.Options{Quality: 92})
		case "WEBP":
			return nativewebp.Encode(f, img, nil)
		}
		return fmt.Errorf("can't make %s from an image", target)
	})
}

// flatten puts transparent pixels on white, since JPEG has no alpha.
func flatten(img image.Image) image.Image {
	out := image.NewRGBA(img.Bounds())
	draw.Draw(out, out.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Over)
	return out
}

// imagesToPDF puts every image on its own page, sized to the image, in one PDF.
func imagesToPDF(paths []string) (string, error) {
	pdf := fpdf.NewCustom(&fpdf.InitType{UnitStr: "pt", Size: fpdf.SizeType{Wd: 595, Ht: 842}})
	pdf.SetMargins(0, 0, 0)
	pdf.SetAutoPageBreak(false, 0)
	for i, p := range paths {
		img, err := loadImage(p)
		if err != nil {
			return "", fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, flatten(img), &jpeg.Options{Quality: 92}); err != nil {
			return "", err
		}
		name := fmt.Sprintf("img%d", i)
		pdf.RegisterImageOptionsReader(name, fpdf.ImageOptions{ImageType: "JPG"}, &buf)
		// 96 dpi pixels to points.
		w, h := float64(img.Bounds().Dx())*0.75, float64(img.Bounds().Dy())*0.75
		pdf.AddPageFormat("P", fpdf.SizeType{Wd: w, Ht: h})
		pdf.ImageOptions(name, 0, 0, w, h, false, fpdf.ImageOptions{ImageType: "JPG"}, 0, "")
	}
	if err := pdf.Error(); err != nil {
		return "", err
	}
	return writeNew(paths[0], "pdf", func(f *os.File) error { return pdf.Output(f) })
}

func ffmpegPath() string {
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		return ""
	}
	return p
}

var mediaArgs = map[string][]string{
	"MP4": {"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-movflags", "+faststart"},
	"GIF": {"-an", "-vf", "fps=12,scale='min(720,iw)':-2:flags=lanczos,split[a][b];[a]palettegen[p];[b][p]paletteuse", "-loop", "0"},
	"MP3": {"-vn", "-c:a", "libmp3lame", "-q:a", "2"},
	"WAV": {"-vn"},
}

func convertMedia(path, target string) (string, error) {
	ffmpeg := ffmpegPath()
	args, ok := mediaArgs[target]
	if ffmpeg == "" || !ok {
		return "", fmt.Errorf("can't make %s (is ffmpeg installed?)", target)
	}
	out := freeName(path, strings.ToLower(target))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	full := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-n", "-i", path}, args...)
	cmd := exec.CommandContext(ctx, ffmpeg, append(full, out)...)
	hideConsole(cmd)
	if msg, err := cmd.CombinedOutput(); err != nil {
		os.Remove(out)
		return "", fmt.Errorf("ffmpeg failed: %s", strings.TrimSpace(lastLine(string(msg), err.Error())))
	}
	return out, nil
}

func lastLine(s, fallback string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if l := lines[len(lines)-1]; l != "" {
		return l
	}
	return fallback
}

// freeName picks "photo.jpg", then "photo (2).jpg", ... beside the source.
func freeName(source, ext string) string {
	base := strings.TrimSuffix(source, filepath.Ext(source))
	for i := 1; ; i++ {
		name := base + "." + ext
		if i > 1 {
			name = fmt.Sprintf("%s (%d).%s", base, i, ext)
		}
		if _, err := os.Lstat(name); errors.Is(err, os.ErrNotExist) {
			return name
		}
	}
}

// writeNew creates a fresh file (never replacing one) and removes it if write fails.
func writeNew(source, ext string, write func(*os.File) error) (string, error) {
	for {
		name := freeName(source, ext)
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue // someone took the name between the check and now
		}
		if err != nil {
			return "", err
		}
		err = write(f)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(name)
			return "", err
		}
		return name, nil
	}
}
