package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestTargets(t *testing.T) {
	cases := []struct {
		paths  []string
		ffmpeg bool
		want   []string
	}{
		{[]string{"a.png"}, false, []string{"JPG", "WEBP", "PDF"}},
		{[]string{"a.JPEG"}, false, []string{"PNG", "WEBP", "PDF"}},
		{[]string{"a.png", "b.jpg"}, false, []string{"PNG", "JPG", "WEBP", "PDF"}},
		{[]string{"a.png", "b.pdf"}, false, nil},
		{[]string{"a.mp4"}, false, nil},
		{[]string{"a.mov"}, true, []string{"MP4", "GIF", "MP3"}},
		{[]string{"a.mp4"}, true, []string{"GIF", "MP3"}},
		{[]string{"a.wav"}, true, []string{"MP3"}},
		{[]string{"a.txt"}, true, nil},
	}
	for _, c := range cases {
		if got := targets(c.paths, c.ffmpeg); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v ffmpeg=%v: got %v want %v", c.paths, c.ffmpeg, got, c.want)
		}
	}
}

func fixture(t *testing.T, dir, name string) string {
	img := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	for i := range img.Pix {
		img.Pix[i] = byte(i)
	}
	img.Set(0, 0, color.NRGBA{0, 0, 0, 0})
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestImageConversionsNeverReplaceFiles(t *testing.T) {
	dir := t.TempDir()
	src := fixture(t, dir, "cat.png")
	before, _ := os.ReadFile(src)
	os.WriteFile(filepath.Join(dir, "cat.jpg"), []byte("already here"), 0o644)
	for _, target := range []string{"JPG", "WEBP", "PNG"} {
		outs, err := convert([]string{src}, target)
		if err != nil || len(outs) != 1 {
			t.Fatalf("%s: %v %v", target, outs, err)
		}
		f, _ := os.Open(outs[0])
		_, _, err = image.Decode(f)
		f.Close()
		if err != nil {
			t.Fatalf("%s output unreadable: %v", target, err)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "cat.jpg")); string(got) != "already here" {
		t.Fatal("an existing file was replaced")
	}
	if _, err := os.Stat(filepath.Join(dir, "cat (2).jpg")); err != nil {
		t.Fatal("expected cat (2).jpg")
	}
	if _, err := os.Stat(filepath.Join(dir, "cat (2).png")); err != nil {
		t.Fatal("PNG to PNG must not overwrite the source")
	}
	if after, _ := os.ReadFile(src); string(after) != string(before) {
		t.Fatal("the original changed")
	}
}

func TestImagesToOnePDF(t *testing.T) {
	dir := t.TempDir()
	a, b := fixture(t, dir, "a.png"), fixture(t, dir, "b.png")
	outs, err := convert([]string{a, b}, "PDF")
	if err != nil || len(outs) != 1 {
		t.Fatal(outs, err)
	}
	data, _ := os.ReadFile(outs[0])
	if !strings.HasPrefix(string(data), "%PDF") || len(regexp.MustCompile(`/Type\s*/Page\b[^s]`).FindAll(data, -1)) != 2 {
		t.Fatal("want a two-page PDF")
	}
}

func TestBadInputLeavesNoFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "fake.png")
	os.WriteFile(bad, []byte("not an image"), 0o644)
	if _, err := convert([]string{bad}, "JPG"); err == nil {
		t.Fatal("want an error")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("a failed conversion left files behind")
	}
}
