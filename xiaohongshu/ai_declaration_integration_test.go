//go:build integration

package xiaohongshu

import (
	"os"
	"testing"
	"time"

	"github.com/xpzouying/xiaohongshu-mcp/browser"
)

// 只验证发布页的AI声明交互，不点击发布。
func TestSetAIContentDeclaration(t *testing.T) {
	videoPath := os.Getenv("XHS_AI_DECLARATION_TEST_VIDEO")
	if videoPath == "" {
		t.Skip("SKIP: 设置 XHS_AI_DECLARATION_TEST_VIDEO 后才运行")
	}

	b := browser.NewBrowser(false)
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	if _, err := NewPublishVideoAction(page); err != nil {
		t.Fatal(err)
	}

	input, err := page.Element(".upload-input")
	if err != nil {
		input, err = page.Element(`input[type="file"]`)
	}
	if err != nil {
		t.Fatal(err)
	}
	input.MustSetFiles(videoPath)

	if _, err := waitForPublishButtonClickable(page, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := setAIContentDeclaration(page); err != nil {
		t.Fatal(err)
	}
	if err := waitForAIContentDeclaration(page, aiDeclarationTimeout); err != nil {
		t.Fatal(err)
	}
	if screenshotPath := os.Getenv("XHS_AI_DECLARATION_SCREENSHOT"); screenshotPath != "" {
		page.MustScreenshot(screenshotPath)
	}
}
