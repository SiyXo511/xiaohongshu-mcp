package xiaohongshu

import (
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"os"
	"testing"
	"time"
)

func TestUploadedVideoCoverWaitsForAsyncThumbnail(t *testing.T) {
	bin := os.Getenv("COVER_TEST_CHROME")
	if bin == "" {
		t.Skip("set COVER_TEST_CHROME for browser regression")
	}
	l := launcher.New().Bin(bin).Headless(true).Leakless(false)
	url, err := l.Launch()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Cleanup()
	b := rod.New().ControlURL(url).MustConnect()
	defer b.MustClose()
	page := b.MustPage()
	defer page.MustClose()
	page.MustSetDocumentContent(`<div id="modal"></div><script>setTimeout(()=>{const c=document.createElement('canvas');c.width=100;c.height=120;const i=new Image();i.alt='已上传封面';i.src=c.toDataURL();document.querySelector('#modal').append(i)},250)</script>`)
	modal := page.Timeout(3 * time.Second).MustElement("#modal")
	if _, err := waitUploadedVideoCover(modal); err != nil {
		t.Fatal(err)
	}
}
