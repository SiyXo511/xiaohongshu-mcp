package xiaohongshu

import (
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

func validateVideoCover(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("封面必须使用绝对路径")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	if !stat.Mode().IsRegular() || stat.Size() > 20<<20 {
		return fmt.Errorf("封面必须是20MiB以内的图片文件")
	}
	config, _, err := image.DecodeConfig(f)
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return fmt.Errorf("封面必须是有效PNG或JPEG图片")
	}
	return nil
}

// Fail closed when the editor is ambiguous. Platform selectors require live acceptance.
func setVideoCover(page *rod.Page, path string) error {
	pp := page.Timeout(90 * time.Second)
	if _, err := pp.Eval(`() => {
      const candidates=[...document.querySelectorAll('button,span,div')].filter(e=>(e.textContent||'').trim()==='我知道了');
      for(const e of candidates){const r=e.getBoundingClientRect();if(r.width>0&&r.height>0){e.click();return true}} return false;
    }`); err != nil {
		return err
	}
	slog.Info("封面步骤：查找编辑入口")
	before, err := videoCoverSources(pp)
	if err != nil {
		return err
	}
	entry, err := pp.Element(".cover-edit-entry")
	if err != nil {
		return fmt.Errorf("未找到视频封面入口: %w", err)
	}
	slog.Info("封面步骤：显露编辑入口")
	parent, parentErr := pp.Element(".cover-plugin-preview .default")
	if parentErr != nil {
		return parentErr
	}
	if err = parent.Hover(); err != nil {
		return fmt.Errorf("悬停封面失败: %w", err)
	}
	if err = entry.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return err
	}
	slog.Info("封面步骤：查找编辑器")
	modal, err := pp.ElementX("//*[self::h6 or self::h2][normalize-space(.)='设置封面']/ancestor::*[.//button[normalize-space(.)='完成']][1]")
	if err != nil {
		return fmt.Errorf("未找到封面编辑弹窗: %w", err)
	}
	slog.Info("封面步骤：查找上传输入框")
	inputs, err := modal.Elements("input[type=file]")
	if err != nil {
		return err
	}
	if len(inputs) != 1 {
		return fmt.Errorf("封面上传输入框不唯一，中止发布")
	}
	if err = inputs[0].SetFiles([]string{path}); err != nil {
		return err
	}
	slog.Info("封面步骤：等待已上传图片")
	uploaded, err := waitUploadedVideoCover(modal)
	if err != nil {
		return fmt.Errorf("未出现已上传封面缩略图: %w", err)
	}
	if err = uploaded.Wait(rod.Eval(`() => this.complete && this.naturalWidth > 0`)); err != nil {
		return err
	}
	if err = uploaded.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return err
	}

	slog.Info("封面步骤：保存图片")
	done, err := modal.ElementR("button", "^(完成|确定|保存)$")
	if err != nil {
		return err
	}
	if err = done.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return err
	}
	slog.Info("封面步骤：等待保存完成")
	if err = modal.WaitInvisible(); err != nil {
		return err
	}
	titleInput, err := pp.Element("div.d-input input")
	if err != nil {
		return err
	}
	if err = titleInput.Hover(); err != nil {
		return err
	}
	slog.Info("封面步骤：验证发布页图片")
	if err = pp.Wait(rod.Eval(`async (before) => {
 const root=document.querySelector('.cover-plugin-preview .default'); if(!root) return false;
 const elements=[root,...root.querySelectorAll('*')];
 const urls=elements.flatMap(e=>{
   const bg=getComputedStyle(e).backgroundImage;
   const match=bg.match(/^url\(["']?(.*?)["']?\)$/);
   return e.tagName==='IMG' && e.complete && e.naturalWidth>100 ? [e.currentSrc||e.src] : match ? [match[1]] : [];
 });
 const fresh=urls.filter(url=>!before.includes(url));
 for(const url of fresh){
   const image=new Image(); image.src=url;
   try { await image.decode(); if(image.naturalWidth>100 && image.naturalHeight>100) return true; } catch {}
 }
 return false;
 }`, before).ByPromise()); err != nil {
		return fmt.Errorf("封面保存后未显示新图片，中止发布: %w", err)
	}

	return nil
}

func videoCoverSources(page *rod.Page) ([]string, error) {
	result, err := page.Eval(`() => {
 const root=document.querySelector('.cover-plugin-preview .default'); if(!root) return [];
 return [root,...root.querySelectorAll('*')].flatMap(e=>{
 const match=getComputedStyle(e).backgroundImage.match(/^url\(["']?(.*?)["']?\)$/);
 return e.tagName==='IMG' ? [e.currentSrc||e.src] : match ? [match[1]] : [];
 });
 }`)
	if err != nil {
		return nil, err
	}
	var sources []string
	for _, item := range result.Value.Arr() {
		sources = append(sources, item.Str())
	}
	return sources, nil
}

func waitUploadedVideoCover(modal *rod.Element) (*rod.Element, error) {
	// Element.Element is a single lookup in Rod; Timeout alone does not retry it.
	if err := modal.Wait(rod.Eval(`() => {
  const img=this.querySelector("img[alt='已上传封面']");
  return !!img && img.complete && img.naturalWidth>0;
 }`)); err != nil {
		return nil, err
	}
	return modal.Element("img[alt='已上传封面']")
}
