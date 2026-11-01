package xiaohongshu

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
	"github.com/pkg/errors"
	"github.com/xpzouying/xiaohongshu-mcp/humanize"
)

const (
	contentManagerURL = "https://creator.xiaohongshu.com/new/note-manager"
	contentEditURL    = "https://creator.xiaohongshu.com/publish/update?id=%s&noteType=video"
)

type ManagedNote struct {
	NoteID      string `json:"note_id"`
	Title       string `json:"title"`
	PublishedAt string `json:"published_at"`
	CoverURL    string `json:"cover_url,omitempty"`
}

type managedNoteRaw struct {
	NoteID      string `json:"noteId"`
	Title       string `json:"title"`
	PublishedAt string `json:"publishedAt"`
	CoverURL    string `json:"coverUrl"`
	Impression  string `json:"impression"`
}

func managedNotesFromRaw(raw []managedNoteRaw) []ManagedNote {
	notes := make([]ManagedNote, 0, len(raw))
	for _, item := range raw {
		noteID := item.NoteID
		if noteID == "" && item.Impression != "" {
			var impression struct {
				NoteTarget struct {
					Value struct {
						NoteID string `json:"noteId"`
					} `json:"value"`
				} `json:"noteTarget"`
			}
			if err := json.Unmarshal([]byte(item.Impression), &impression); err == nil {
				noteID = impression.NoteTarget.Value.NoteID
			}
		}
		notes = append(notes, ManagedNote{
			NoteID:      noteID,
			Title:       item.Title,
			PublishedAt: item.PublishedAt,
			CoverURL:    item.CoverURL,
		})
	}
	return notes
}

func uniqueManagedNote(notes []ManagedNote, wantedID string) (ManagedNote, error) {
	var matches []ManagedNote
	for _, note := range notes {
		if note.NoteID == wantedID {
			matches = append(matches, note)
		}
	}
	if len(matches) == 0 {
		return ManagedNote{}, errors.Errorf("未找到笔记 ID %s", wantedID)
	}
	if len(matches) > 1 {
		return ManagedNote{}, fmt.Errorf("笔记 ID %s 不唯一，拒绝按序号猜测", wantedID)
	}
	return matches[0], nil
}

func UniqueManagedNote(notes []ManagedNote, wantedID string) (ManagedNote, error) {
	return uniqueManagedNote(notes, wantedID)
}

func CurrentAccount(ctx context.Context, page *rod.Page) (*CurrentUser, error) {
	login := NewLogin(page)
	loggedIn, err := login.CheckLoginStatus(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "检查小红书登录状态失败")
	}
	if !loggedIn {
		return nil, errors.New("小红书登录失效，请重新扫码登录")
	}
	user, err := login.CurrentUser(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "读取小红书当前账号失败")
	}
	if user.UserID == "" {
		return nil, errors.New("小红书当前账号缺少稳定用户 ID")
	}
	return user, nil
}

func SessionFingerprint(page *rod.Page) (string, error) {
	cookies, err := page.Browser().GetCookies()
	if err != nil {
		return "", errors.Wrap(err, "读取小红书登录会话失败")
	}
	return sessionFingerprintFromCookies(cookies), nil
}

func sessionFingerprintFromCookies(cookies []*proto.NetworkCookie) string {
	lines := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		switch cookie.Name {
		case "galaxy_creator_session_id", "web_session", "a1":
		default:
			continue
		}
		lines = append(lines, strings.Join([]string{
			cookie.Name,
			cookie.Value,
			cookie.Domain,
			cookie.Path,
		}, "\x00"))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\x01")))
	return hex.EncodeToString(sum[:])
}

type ContentAction struct {
	page *rod.Page
}

func NewContentAction(page *rod.Page) *ContentAction {
	return &ContentAction{page: page.Timeout(2 * time.Minute)}
}

func (a *ContentAction) ListManagedNotes(ctx context.Context) ([]ManagedNote, error) {
	page := a.page.Context(ctx).Timeout(2 * time.Minute)
	if err := ensureCreatorSession(page); err != nil {
		return nil, err
	}
	if err := page.Navigate(contentManagerURL); err != nil {
		return nil, errors.Wrap(err, "打开小红书笔记管理页失败")
	}
	_ = page.WaitLoad()
	if err := waitManagedNoteCards(ctx, page); err != nil {
		return nil, err
	}
	result, err := page.Eval(`() => JSON.stringify(
		[...document.querySelectorAll(".note-card")].map((card) => {
			const clean = (value) => (value || "").replace(/\s+/g, " ").trim()
			const cover = card.querySelector(".note-card__cover")
			const image = cover && cover.querySelector("img")
			const background = cover ? getComputedStyle(cover).backgroundImage : ""
			const match = background.match(/^url\(["']?(.*?)["']?\)$/)
			return {
				title: clean(card.querySelector(".note-card__title")?.innerText),
				publishedAt: clean(card.querySelector(".note-card__time")?.innerText),
				coverUrl: image?.src || (match ? match[1] : ""),
				impression: card.getAttribute("data-impression") || "",
			}
		})
	)`)
	if err != nil {
		return nil, errors.Wrap(err, "读取小红书笔记列表失败")
	}
	var raw []managedNoteRaw
	if err := json.Unmarshal([]byte(result.Value.String()), &raw); err != nil {
		return nil, errors.Wrap(err, "解析小红书笔记列表失败")
	}
	return managedNotesFromRaw(raw), nil
}

type ContentEditor struct {
	page    *rod.Page
	title   *rod.Element
	content *rod.Element
}

func (a *ContentAction) OpenEditor(ctx context.Context, noteID string) (*ContentEditor, error) {
	page := a.page.Context(ctx).Timeout(2 * time.Minute)
	if err := ensureCreatorSession(page); err != nil {
		return nil, err
	}
	target := fmt.Sprintf(contentEditURL, noteID)
	if err := page.Navigate(target); err != nil {
		return nil, errors.Wrap(err, "打开小红书笔记编辑页失败")
	}
	_ = page.WaitLoad()
	return a.CurrentEditor(ctx)
}

func (a *ContentAction) CurrentEditor(ctx context.Context) (*ContentEditor, error) {
	page := a.page.Context(ctx).Timeout(2 * time.Minute)
	title, err := waitContentElement(ctx, page, []string{
		`input[placeholder*="填写标题"]`,
		`div.d-input input`,
	})
	if err != nil {
		return nil, errors.Wrap(err, "小红书编辑页缺少标题输入框")
	}
	content, err := getContentElement(page, 20*time.Second)
	if err != nil {
		return nil, errors.Wrap(err, "小红书编辑页缺少正文输入框")
	}
	return &ContentEditor{page: page, title: title, content: content}, nil
}

func (e *ContentEditor) Current() (string, string, error) {
	title, err := e.title.Property("value")
	if err != nil {
		return "", "", errors.Wrap(err, "读取小红书编辑页标题失败")
	}
	content, err := e.content.Text()
	if err != nil {
		return "", "", errors.Wrap(err, "读取小红书编辑页正文失败")
	}
	return normalizeContentText(title.Str()), normalizeContentText(content), nil
}

func (e *ContentEditor) Replace(ctx context.Context, title, content *string) error {
	if title != nil {
		if err := replaceContentText(ctx, e.title, *title, false); err != nil {
			return errors.Wrap(err, "填写小红书新标题失败")
		}
	}
	if content != nil {
		if err := replaceContentText(ctx, e.content, *content, true); err != nil {
			return errors.Wrap(err, "填写小红书新正文失败")
		}
	}
	return nil
}

func (e *ContentEditor) Verify(title, content *string, originalTitle, originalContent string) error {
	gotTitle, gotContent, err := e.Current()
	if err != nil {
		return err
	}
	wantTitle := originalTitle
	if title != nil {
		wantTitle = *title
	}
	wantContent := originalContent
	if content != nil {
		wantContent = *content
	}
	if gotTitle != normalizeContentText(wantTitle) ||
		gotContent != normalizeContentText(wantContent) {
		return fmt.Errorf("小红书编辑页回读值与预览请求不一致")
	}
	return nil
}

func (e *ContentEditor) Page() *rod.Page {
	return e.page
}

func (e *ContentEditor) FindButton(ctx context.Context, labels ...string) (*rod.Element, error) {
	deadline := time.Now().Add(20 * time.Second)
	for {
		items, err := e.page.Elements("button,[role=button]")
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if !isElementVisible(item) {
				continue
			}
			text, err := item.Text()
			if err != nil {
				continue
			}
			for _, label := range labels {
				if normalizeContentText(text) == label {
					return item, nil
				}
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("小红书编辑页未找到按钮 %s", strings.Join(labels, "/"))
		}
		if err := waitContentPoll(ctx); err != nil {
			return nil, err
		}
	}
}

func (e *ContentEditor) Submit(ctx context.Context) error {
	button, err := e.FindButton(ctx, "发布", "更新")
	if err != nil {
		return err
	}
	if err := humanize.Click(button); err != nil {
		return errors.Wrap(err, "点击小红书提交修改失败")
	}
	return waitContentPoll(ctx)
}

func (a *ContentAction) OpenDeletion(ctx context.Context, noteID string) (string, error) {
	page := a.page.Context(ctx).Timeout(2 * time.Minute)
	if err := ensureCreatorSession(page); err != nil {
		return "", err
	}
	if err := page.Navigate(contentManagerURL); err != nil {
		return "", errors.Wrap(err, "打开小红书笔记管理页失败")
	}
	_ = page.WaitLoad()
	if err := waitManagedNoteCards(ctx, page); err != nil {
		return "", err
	}
	result, err := page.Eval(`(noteID) => {
		for (const card of document.querySelectorAll(".note-card")) {
			let impression = null
			try {
				impression = JSON.parse(card.getAttribute("data-impression") || "")
			} catch (_) {
				continue
			}
			if (impression?.noteTarget?.value?.noteId !== noteID) continue
			const button = card.querySelector(".note-card__action-btn--del")
			if (!button) return false
			button.click()
			return true
		}
		return false
	}`, noteID)
	if err != nil {
		return "", errors.Wrap(err, "定位小红书删除按钮失败")
	}
	if !result.Value.Bool() {
		return "", fmt.Errorf("未找到笔记 ID %s 的删除按钮", noteID)
	}
	return waitXHSContentDialog(ctx, page, "删除")
}

func (a *ContentAction) ContentDialogText(contains string) (string, error) {
	return visibleXHSContentDialogText(a.page, contains)
}

func (a *ContentAction) ConfirmDeletion(ctx context.Context) error {
	button, err := visibleXHSContentDialogButton(a.page, "确定")
	if err != nil {
		return err
	}
	if err := humanize.Click(button); err != nil {
		return errors.Wrap(err, "点击小红书删除确定失败")
	}
	return waitContentPoll(ctx)
}

func (a *ContentAction) NoteMissing(ctx context.Context, noteID string) (bool, error) {
	deadline := time.Now().Add(30 * time.Second)
	for {
		result, err := a.page.Eval(`(noteID) => {
			let count = 0
			for (const card of document.querySelectorAll(".note-card")) {
				count++
				let impression = null
				try {
					impression = JSON.parse(card.getAttribute("data-impression") || "")
				} catch (_) {
					continue
				}
				if (impression?.noteTarget?.value?.noteId === noteID) {
					return JSON.stringify({hasTarget:true, count})
				}
			}
			return JSON.stringify({
				hasTarget:false,
				count,
				body:(document.body?.innerText || "").replace(/\s+/g, " ").trim()
			})
		}`, noteID)
		if err != nil {
			return false, err
		}
		var state struct {
			HasTarget bool   `json:"hasTarget"`
			Count     int    `json:"count"`
			Body      string `json:"body"`
		}
		if err := json.Unmarshal([]byte(result.Value.String()), &state); err != nil {
			return false, err
		}
		if xhsNoteMissingConfirmed(state.HasTarget, state.Count, state.Body) {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, fmt.Errorf("已点击删除，但页面未返回可验证结果")
		}
		if err := waitContentPoll(ctx); err != nil {
			return false, err
		}
	}
}

func xhsNoteMissingConfirmed(hasTarget bool, cardCount int, body string) bool {
	if hasTarget {
		return false
	}
	return strings.Contains(normalizeContentText(body), "删除成功") || cardCount > 0
}

func waitManagedNoteCards(ctx context.Context, page *rod.Page) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		result, err := page.Eval(`() => document.querySelectorAll(".note-card").length`)
		if err == nil && result.Value.Int() > 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待小红书笔记管理列表超时")
		}
		if err := waitContentPoll(ctx); err != nil {
			return err
		}
	}
}

func waitContentElement(
	ctx context.Context,
	page *rod.Page,
	selectors []string,
) (*rod.Element, error) {
	deadline := time.Now().Add(20 * time.Second)
	for {
		for _, selector := range selectors {
			items, err := page.Elements(selector)
			if err != nil {
				continue
			}
			for _, item := range items {
				if isElementVisible(item) {
					return item, nil
				}
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("等待小红书编辑控件超时")
		}
		if err := waitContentPoll(ctx); err != nil {
			return nil, err
		}
	}
}

func replaceContentText(
	ctx context.Context,
	element *rod.Element,
	value string,
	contentEditable bool,
) error {
	if err := humanize.Click(element); err != nil {
		return err
	}
	actions, err := element.KeyActions()
	if err != nil {
		return err
	}
	modifier := input.MetaLeft
	if runtime.GOOS != "darwin" {
		modifier = input.ControlLeft
	}
	if err := actions.Press(modifier).Type(input.KeyA).Do(); err != nil {
		return err
	}
	if contentEditable {
		if err := element.Page().Keyboard.Press(input.Delete); err != nil {
			return err
		}
	}
	return humanize.Type(ctx, element, value)
}

func waitXHSContentDialog(ctx context.Context, page *rod.Page, contains string) (string, error) {
	deadline := time.Now().Add(20 * time.Second)
	for {
		if text, err := visibleXHSContentDialogText(page, contains); err == nil && text != "" {
			return text, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("等待小红书确认框超时")
		}
		if err := waitContentPoll(ctx); err != nil {
			return "", err
		}
	}
}

func visibleXHSContentDialogText(page *rod.Page, contains string) (string, error) {
	items, err := page.Elements(".d-modal,.d-dialog,[role=dialog]")
	if err != nil {
		return "", err
	}
	for _, item := range items {
		if !isElementVisible(item) {
			continue
		}
		text, err := item.Text()
		if err == nil && strings.Contains(normalizeContentText(text), contains) {
			return normalizeContentText(text), nil
		}
	}
	return "", nil
}

func visibleXHSContentDialogButton(page *rod.Page, label string) (*rod.Element, error) {
	items, err := page.Elements(".d-modal button,.d-dialog button,[role=dialog] button")
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if !isElementVisible(item) {
			continue
		}
		text, err := item.Text()
		if err == nil && normalizeContentText(text) == label {
			return item, nil
		}
	}
	return nil, fmt.Errorf("小红书确认框中未找到 %s", label)
}

func normalizeContentText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\u200b", "")
	value = strings.ReplaceAll(value, "\ufeff", "")
	value = strings.ReplaceAll(value, "\u00a0", " ")
	return strings.TrimSpace(value)
}

func waitContentPoll(ctx context.Context) error {
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
