//go:build integration

package xiaohongshu

import (
	"context"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
)

func TestReplaceContentTextClearsExistingTitle(t *testing.T) {
	bin, err := browser.EnsureBrowser()
	if err != nil {
		t.Skipf("SKIP: 浏览器不可用: %v", err)
	}

	url := launcher.New().Bin(bin).Headless(true).MustLaunch()
	rodBrowser := rod.New().ControlURL(url).MustConnect()
	defer rodBrowser.MustClose()

	page := rodBrowser.MustPage("about:blank")
	page.MustSetDocumentContent(`
		<input id="title" value="把想念寄出去" style="width:400px;height:40px">
		<script>
			document.querySelector("#title").addEventListener("keydown", (event) => {
				if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "a") {
					event.preventDefault()
				}
			})
		</script>
	`)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	input := page.MustElement("#title")
	if err := replaceContentText(ctx, input, "把想念寄出去·预览", false); err != nil {
		t.Fatalf("替换标题失败: %v", err)
	}
	if got := input.MustProperty("value").Str(); got != "把想念寄出去·预览" {
		t.Fatalf("标题未完整替换: got=%q", got)
	}
}

func TestReplaceContentTextReplacesExistingBody(t *testing.T) {
	bin, err := browser.EnsureBrowser()
	if err != nil {
		t.Skipf("SKIP: 浏览器不可用: %v", err)
	}

	url := launcher.New().Bin(bin).Headless(true).MustLaunch()
	rodBrowser := rod.New().ControlURL(url).MustConnect()
	defer rodBrowser.MustClose()

	page := rodBrowser.MustPage("about:blank")
	page.MustSetDocumentContent(`
		<div id="body" contenteditable="true" style="width:400px;height:80px">旧正文</div>
	`)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	body := page.MustElement("#body")
	if err := replaceContentText(ctx, body, "这是新正文。", true); err != nil {
		t.Fatalf("替换正文失败: %v", err)
	}
	if got := normalizeContentText(body.MustText()); got != "这是新正文。" {
		t.Fatalf("正文未完整替换: got=%q", got)
	}
}
