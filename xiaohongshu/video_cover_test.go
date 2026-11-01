package xiaohongshu

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestVideoCoverValidation(t *testing.T) {
	if validateVideoCover("relative.png") == nil {
		t.Fatal("relative path accepted")
	}
	dir := t.TempDir()
	invalid := filepath.Join(dir, "invalid.png")
	if err := os.WriteFile(invalid, []byte("not an image"), 0600); err != nil {
		t.Fatal(err)
	}
	if validateVideoCover(invalid) == nil {
		t.Fatal("invalid bytes accepted")
	}
	if validateVideoCover(dir) == nil {
		t.Fatal("directory accepted")
	}
	valid := filepath.Join(dir, "cover.png")
	f, err := os.Create(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(f, image.NewRGBA(image.Rect(0, 0, 300, 400))); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err = validateVideoCover(valid); err != nil {
		t.Fatal(err)
	}
}
