package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

type contentOperationKind string

const (
	contentOperationEdit   contentOperationKind = "edit"
	contentOperationDelete contentOperationKind = "delete"
)

const (
	contentReceiptSubmitting = "submitting"
	contentReceiptSubmitted  = "submitted"
	contentReceiptUnknown    = "unknown"
	contentOperationTTL      = 30 * time.Minute
)

type XHSContentTarget struct {
	NoteID      string `json:"note_id"`
	Title       string `json:"title"`
	Content     string `json:"content"`
	PublishedAt string `json:"published_at"`
	CoverURL    string `json:"cover_url,omitempty"`
	AccountID   string `json:"account_id"`
	AccountName string `json:"account_name"`
}

type XHSEditRequest struct {
	NoteID            string  `json:"note_id"`
	ExpectedAccountID string  `json:"expected_account_id"`
	Title             *string `json:"title,omitempty"`
	Content           *string `json:"content,omitempty"`
}

type XHSDeleteRequest struct {
	NoteID            string `json:"note_id"`
	ExpectedAccountID string `json:"expected_account_id"`
}

type XHSContentSupport struct {
	Title      bool   `json:"title"`
	Content    bool   `json:"content"`
	Video      bool   `json:"video"`
	Cover      bool   `json:"cover"`
	Visibility bool   `json:"visibility"`
	Note       string `json:"note,omitempty"`
}

type XHSPrepareEditResult struct {
	OperationID    string            `json:"operation_id"`
	Stage          string            `json:"stage"`
	Target         XHSContentTarget  `json:"target"`
	Title          *string           `json:"title,omitempty"`
	Content        *string           `json:"content,omitempty"`
	Support        XHSContentSupport `json:"support"`
	ExpiresAt      time.Time         `json:"expires_at"`
	ScreenshotPath string            `json:"screenshot_path"`
	Message        string            `json:"message"`
}

type XHSPrepareDeleteResult struct {
	OperationID    string            `json:"operation_id"`
	Stage          string            `json:"stage"`
	Target         XHSContentTarget  `json:"target"`
	PlatformNotice string            `json:"platform_notice"`
	Support        XHSContentSupport `json:"support"`
	ExpiresAt      time.Time         `json:"expires_at"`
	ScreenshotPath string            `json:"screenshot_path"`
	Message        string            `json:"message"`
}

type XHSConfirmContentResult struct {
	OperationID string    `json:"operation_id"`
	Kind        string    `json:"kind"`
	NoteID      string    `json:"note_id"`
	Stage       string    `json:"stage"`
	Success     bool      `json:"success"`
	Submitted   time.Time `json:"submitted_at,omitempty"`
	Message     string    `json:"message"`
}

type xhsContentOperation struct {
	OperationID        string
	Kind               contentOperationKind
	NoteID             string
	ExpectedAccountID  string
	AccountID          string
	TargetFingerprint  string
	SessionFingerprint string
	Current            XHSContentTarget
	Title              *string
	Content            *string
	Page               *rod.Page
	ExpiresAt          time.Time
	Attempted          bool
	Active             bool
}

type xhsContentReceipt struct {
	OperationID string               `json:"operation_id"`
	Kind        contentOperationKind `json:"kind"`
	NoteID      string               `json:"note_id"`
	AccountID   string               `json:"account_id"`
	Stage       string               `json:"stage"`
	Submitted   time.Time            `json:"submitted_at,omitempty"`
	Message     string               `json:"message"`
}

func validateXHSEditRequest(request XHSEditRequest) (XHSEditRequest, error) {
	request.NoteID = strings.TrimSpace(request.NoteID)
	request.ExpectedAccountID = strings.TrimSpace(request.ExpectedAccountID)
	if request.NoteID == "" {
		return request, fmt.Errorf("必须提供稳定的笔记 ID")
	}
	if request.ExpectedAccountID == "" {
		return request, fmt.Errorf("必须提供准备时看到的账号 ID")
	}
	if request.Title == nil && request.Content == nil {
		return request, fmt.Errorf("至少需要修改标题或正文")
	}
	if request.Title != nil {
		title := strings.TrimSpace(*request.Title)
		if title == "" {
			return request, fmt.Errorf("标题不能为空")
		}
		if len([]rune(title)) > 20 || strings.ContainsAny(title, "\r\n") {
			return request, fmt.Errorf("小红书标题必须是 1 到 20 个字符的单行文本")
		}
		request.Title = &title
	}
	if request.Content != nil {
		content := strings.TrimSpace(*request.Content)
		if len([]rune(content)) > 1000 {
			return request, fmt.Errorf("小红书正文不能超过 1000 个字符")
		}
		request.Content = &content
	}
	return request, nil
}

func validateXHSDeleteRequest(request XHSDeleteRequest) (XHSDeleteRequest, error) {
	request.NoteID = strings.TrimSpace(request.NoteID)
	request.ExpectedAccountID = strings.TrimSpace(request.ExpectedAccountID)
	if request.NoteID == "" || request.ExpectedAccountID == "" {
		return request, fmt.Errorf("必须提供稳定的笔记 ID 和 expected_account_id")
	}
	return request, nil
}

func (o *xhsContentOperation) validate(
	now time.Time,
	accountID, targetFingerprint, sessionFingerprint string,
) error {
	if !o.Active {
		return fmt.Errorf("操作不存在、服务已重启或已被新的操作替换")
	}
	if now.After(o.ExpiresAt) {
		return fmt.Errorf("操作已超过 30 分钟，请重新准备")
	}
	if accountID == "" || accountID != o.AccountID || accountID != o.ExpectedAccountID {
		return fmt.Errorf("登录账号与准备时不一致，拒绝执行")
	}
	if targetFingerprint == "" || targetFingerprint != o.TargetFingerprint {
		return fmt.Errorf("作品内容与准备时不一致，拒绝执行")
	}
	if sessionFingerprint == "" || sessionFingerprint != o.SessionFingerprint {
		return fmt.Errorf("登录会话与准备时不一致，拒绝执行")
	}
	return nil
}

func (s *XiaohongshuService) activateContentOperation(operation *xhsContentOperation) {
	if previous := s.activeContentOperation; previous != nil {
		previous.Active = false
		if previous.Page != nil {
			_ = previous.Page.Close()
		}
		s.closeContentBrowser()
	}
	operation.Active = true
	s.activeContentOperation = operation
}

func (s *XiaohongshuService) requireActiveContentOperation(
	operationID string,
	kind contentOperationKind,
) (*xhsContentOperation, error) {
	operation := s.activeContentOperation
	if operation == nil || !operation.Active ||
		operation.OperationID != operationID || operation.Kind != kind {
		return nil, fmt.Errorf("操作不存在、服务已重启或已被新的操作替换")
	}
	return operation, nil
}

func (s *XiaohongshuService) closeContentOperation() {
	if operation := s.activeContentOperation; operation != nil {
		operation.Active = false
		if operation.Page != nil {
			_ = operation.Page.Close()
		}
		s.closeContentBrowser()
	}
	s.activeContentOperation = nil
}

func (s *XiaohongshuService) closeContentBrowser() {
	browser := s.contentBrowser
	s.contentBrowser = nil
	s.contentPage = nil
	if browser == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	browser.Close()
}

func xhsContentFingerprint(target XHSContentTarget) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		target.NoteID,
		target.Title,
		target.Content,
		target.PublishedAt,
		target.AccountID,
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func (s *XiaohongshuService) contentRecipientDir() string {
	dir := s.contentDataDir
	if dir == "" {
		dir = filepath.Dir(cookiesPath())
	}
	return filepath.Join(dir, "content-receipts")
}

func cookiesPath() string {
	return os.Getenv("COOKIES_PATH")
}

func (s *XiaohongshuService) writeContentReceipt(receipt xhsContentReceipt) error {
	dir := s.contentRecipientDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建小红书作品管理回执目录失败: %w", err)
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, receipt.OperationID+".json")
	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return fmt.Errorf("写入小红书作品管理回执失败: %w", err)
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("保存小红书作品管理回执失败: %w", err)
	}
	return nil
}

func (s *XiaohongshuService) readContentReceipt(operationID string) (xhsContentReceipt, bool, error) {
	path := filepath.Join(s.contentRecipientDir(), operationID+".json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return xhsContentReceipt{}, false, nil
	}
	if err != nil {
		return xhsContentReceipt{}, false, fmt.Errorf("读取小红书作品管理回执失败: %w", err)
	}
	var receipt xhsContentReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return xhsContentReceipt{}, false, fmt.Errorf("小红书作品管理回执损坏，拒绝重复执行: %w", err)
	}
	if receipt.OperationID != operationID {
		return xhsContentReceipt{}, false, fmt.Errorf("小红书作品管理回执不一致，拒绝重复执行")
	}
	return receipt, true, nil
}

func xhsContentReceiptResult(receipt xhsContentReceipt) (*XHSConfirmContentResult, error) {
	if receipt.Stage != contentReceiptSubmitted {
		return nil, fmt.Errorf("作品操作结果未知；请先检查创作者中心，不要自动重试: %s", receipt.Message)
	}
	return &XHSConfirmContentResult{
		OperationID: receipt.OperationID,
		Kind:        string(receipt.Kind),
		NoteID:      receipt.NoteID,
		Stage:       receipt.Stage,
		Success:     true,
		Submitted:   receipt.Submitted,
		Message:     receipt.Message,
	}, nil
}

func noteTarget(note xiaohongshu.ManagedNote, accountID, accountName, content string) XHSContentTarget {
	return XHSContentTarget{
		NoteID:      note.NoteID,
		Title:       note.Title,
		Content:     content,
		PublishedAt: note.PublishedAt,
		CoverURL:    note.CoverURL,
		AccountID:   accountID,
		AccountName: accountName,
	}
}

func (s *XiaohongshuService) ensureContentPage() (*rod.Page, error) {
	if s.contentPage == nil {
		page, err := s.newContentBrowserPage()
		if err != nil {
			return nil, err
		}
		s.contentPage = page
	}
	if s.contentPage == nil {
		return nil, fmt.Errorf("无法创建小红书作品管理页面")
	}
	return s.contentPage, nil
}

func (s *XiaohongshuService) newContentAuxPage() (*rod.Page, error) {
	return s.newContentBrowserPage()
}

func (s *XiaohongshuService) newContentBrowserPage() (*rod.Page, error) {
	page, err := s.tryNewContentBrowserPage()
	if err == nil {
		return page, nil
	}
	s.closeContentBrowser()
	page, retryErr := s.tryNewContentBrowserPage()
	if retryErr == nil {
		return page, nil
	}
	return nil, fmt.Errorf("创建小红书作品管理页面失败: %w; 重建后仍失败: %v", err, retryErr)
}

func (s *XiaohongshuService) tryNewContentBrowserPage() (page *rod.Page, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			page = nil
			err = fmt.Errorf("浏览器创建页面失败: %v", recovered)
		}
	}()
	if s.contentBrowser == nil {
		s.contentBrowser = newBrowser()
	}
	page = s.contentBrowser.NewPage()
	if page == nil {
		return nil, fmt.Errorf("浏览器返回空页面")
	}
	return page, nil
}

func (s *XiaohongshuService) currentContentAccount(ctx context.Context) (*xiaohongshu.CurrentUser, error) {
	page, err := s.newContentAuxPage()
	if err != nil {
		return nil, err
	}
	defer page.Close()
	return xiaohongshu.CurrentAccount(ctx, page)
}

func (s *XiaohongshuService) locateContentTarget(
	ctx context.Context,
	noteID string,
	account *xiaohongshu.CurrentUser,
) (XHSContentTarget, error) {
	page, err := s.newContentAuxPage()
	if err != nil {
		return XHSContentTarget{}, err
	}
	defer page.Close()

	action := xiaohongshu.NewContentAction(page)
	notes, err := action.ListManagedNotes(ctx)
	if err != nil {
		return XHSContentTarget{}, err
	}
	note, err := xiaohongshu.UniqueManagedNote(notes, noteID)
	if err != nil {
		return XHSContentTarget{}, err
	}
	editor, err := action.OpenEditor(ctx, noteID)
	if err != nil {
		return XHSContentTarget{}, err
	}
	title, content, err := editor.Current()
	if err != nil {
		return XHSContentTarget{}, err
	}
	if strings.TrimSpace(note.Title) != "" && title != note.Title {
		return XHSContentTarget{}, fmt.Errorf("管理列表与编辑页标题不一致，拒绝按可能变化的作品继续")
	}
	note.Title = title
	return noteTarget(note, account.UserID, account.Nickname, content), nil
}

func (s *XiaohongshuService) saveContentScreenshot(name string, png []byte) (string, error) {
	dir := filepath.Join(s.contentDataDir, "screenshots")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建小红书作品管理截图目录失败: %w", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, png, 0o600); err != nil {
		return "", fmt.Errorf("保存小红书作品管理截图失败: %w", err)
	}
	return path, nil
}

func randomContentOperationID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("生成作品管理操作 ID 失败: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func (s *XiaohongshuService) recordUnknownContentOperation(
	submitting xhsContentReceipt,
	cause error,
) (*XHSConfirmContentResult, error) {
	unknown := submitting
	unknown.Stage = contentReceiptUnknown
	unknown.Message = "小红书作品操作结果未知；请先检查创作者中心，不要自动重试: " + cause.Error()
	if err := s.writeContentReceipt(unknown); err != nil {
		return nil, fmt.Errorf("小红书作品操作结果未知，且保存回执失败: %v; %w", cause, err)
	}
	return nil, fmt.Errorf("%s", unknown.Message)
}

func (s *XiaohongshuService) PrepareContentEdit(
	ctx context.Context,
	request XHSEditRequest,
) (*XHSPrepareEditResult, error) {
	request, err := validateXHSEditRequest(request)
	if err != nil {
		return nil, err
	}

	s.contentMu.Lock()
	defer s.contentMu.Unlock()

	s.closeContentOperation()
	account, err := s.currentContentAccount(ctx)
	if err != nil {
		return nil, err
	}
	if account.UserID != request.ExpectedAccountID {
		return nil, fmt.Errorf("当前登录账号 %s 与 expected_account_id 不一致，未准备编辑", account.UserID)
	}
	target, err := s.locateContentTarget(ctx, request.NoteID, account)
	if err != nil {
		return nil, err
	}

	page, err := s.ensureContentPage()
	if err != nil {
		return nil, err
	}
	editor, err := xiaohongshu.NewContentAction(page).OpenEditor(ctx, request.NoteID)
	if err != nil {
		return nil, err
	}
	currentTitle, currentContent, err := editor.Current()
	if err != nil {
		return nil, err
	}
	if currentTitle != target.Title || currentContent != target.Content {
		return nil, fmt.Errorf("编辑页内容与刚读取的作品不一致，未准备修改")
	}
	if err := editor.Replace(ctx, request.Title, request.Content); err != nil {
		return nil, err
	}
	if err := editor.Verify(request.Title, request.Content, target.Title, target.Content); err != nil {
		return nil, err
	}
	png, err := page.Screenshot(true, nil)
	if err != nil {
		return nil, fmt.Errorf("截取小红书编辑预览失败: %w", err)
	}
	id, err := randomContentOperationID()
	if err != nil {
		return nil, err
	}
	screenshotPath, err := s.saveContentScreenshot("content-edit-preview-"+id+".png", png)
	if err != nil {
		return nil, err
	}
	session, err := xiaohongshu.SessionFingerprint(page)
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(contentOperationTTL)
	s.activateContentOperation(&xhsContentOperation{
		OperationID:        id,
		Kind:               contentOperationEdit,
		NoteID:             request.NoteID,
		ExpectedAccountID:  request.ExpectedAccountID,
		AccountID:          account.UserID,
		TargetFingerprint:  xhsContentFingerprint(target),
		SessionFingerprint: session,
		Current:            target,
		Title:              request.Title,
		Content:            request.Content,
		Page:               page,
		ExpiresAt:          expiresAt,
	})

	return &XHSPrepareEditResult{
		OperationID: id,
		Stage:       "ready",
		Target:      target,
		Title:       request.Title,
		Content:     request.Content,
		Support: XHSContentSupport{
			Title: true, Content: true, Video: false, Cover: false, Visibility: false,
			Note: "第一版只提交标题和正文；编辑页虽有文件控件，但未实现视频替换，不做删旧重发",
		},
		ExpiresAt:      expiresAt,
		ScreenshotPath: screenshotPath,
		Message:        "小红书编辑页已填写但尚未提交；请核对截图后调用 confirm_content_edit",
	}, nil
}

func (s *XiaohongshuService) ConfirmContentEdit(
	ctx context.Context,
	operationID string,
) (*XHSConfirmContentResult, error) {
	s.contentMu.Lock()
	defer s.contentMu.Unlock()

	if receipt, found, err := s.readContentReceipt(operationID); err != nil {
		return nil, err
	} else if found {
		return xhsContentReceiptResult(receipt)
	}
	operation, err := s.requireActiveContentOperation(operationID, contentOperationEdit)
	if err != nil {
		return nil, err
	}
	if operation.Attempted {
		return nil, fmt.Errorf("该操作已经尝试提交，程序不会再次点击")
	}
	account, err := s.currentContentAccount(ctx)
	if err != nil {
		return nil, err
	}
	target, err := s.locateContentTarget(ctx, operation.NoteID, account)
	if err != nil {
		return nil, err
	}
	session, err := xiaohongshu.SessionFingerprint(operation.Page)
	if err != nil {
		return nil, err
	}
	if err := operation.validate(time.Now(), account.UserID, xhsContentFingerprint(target), session); err != nil {
		return nil, err
	}
	editor, err := xiaohongshu.NewContentAction(operation.Page).CurrentEditor(ctx)
	if err != nil {
		return nil, err
	}
	if err := editor.Verify(operation.Title, operation.Content, operation.Current.Title, operation.Current.Content); err != nil {
		return nil, err
	}

	operation.Attempted = true
	submitting := xhsContentReceipt{
		OperationID: operation.OperationID,
		Kind:        contentOperationEdit,
		NoteID:      operation.NoteID,
		AccountID:   operation.AccountID,
		Stage:       contentReceiptSubmitting,
		Message:     "已开始提交小红书编辑，尚未确认结果",
	}
	if err := s.writeContentReceipt(submitting); err != nil {
		operation.Attempted = false
		return nil, err
	}
	if err := editor.Submit(ctx); err != nil {
		return s.recordUnknownContentOperation(submitting, err)
	}
	if err := waitXHSEditMutation(ctx, operation.Page); err != nil {
		return s.recordUnknownContentOperation(submitting, err)
	}
	now := time.Now()
	receipt := xhsContentReceipt{
		OperationID: operation.OperationID,
		Kind:        contentOperationEdit,
		NoteID:      operation.NoteID,
		AccountID:   operation.AccountID,
		Stage:       contentReceiptSubmitted,
		Submitted:   now,
		Message:     "小红书已接受编辑提交；是否审核通过或最终生效需回查笔记管理页",
	}
	if err := s.writeContentReceipt(receipt); err != nil {
		return nil, fmt.Errorf("编辑已提交，但保存回执失败: %w", err)
	}
	return xhsContentReceiptResult(receipt)
}

func (s *XiaohongshuService) PrepareContentDelete(
	ctx context.Context,
	request XHSDeleteRequest,
) (*XHSPrepareDeleteResult, error) {
	request, err := validateXHSDeleteRequest(request)
	if err != nil {
		return nil, err
	}

	s.contentMu.Lock()
	defer s.contentMu.Unlock()

	s.closeContentOperation()
	account, err := s.currentContentAccount(ctx)
	if err != nil {
		return nil, err
	}
	if account.UserID != request.ExpectedAccountID {
		return nil, fmt.Errorf("当前登录账号 %s 与 expected_account_id 不一致，未准备删除", account.UserID)
	}
	target, err := s.locateContentTarget(ctx, request.NoteID, account)
	if err != nil {
		return nil, err
	}
	page, err := s.ensureContentPage()
	if err != nil {
		return nil, err
	}
	action := xiaohongshu.NewContentAction(page)
	modalText, err := action.OpenDeletion(ctx, request.NoteID)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(modalText, "删除后将无法恢复") {
		return nil, fmt.Errorf("小红书删除确认框未明确提示不可恢复，停止准备删除")
	}
	png, err := page.Screenshot(true, nil)
	if err != nil {
		return nil, fmt.Errorf("截取小红书删除预览失败: %w", err)
	}
	id, err := randomContentOperationID()
	if err != nil {
		return nil, err
	}
	screenshotPath, err := s.saveContentScreenshot("content-delete-preview-"+id+".png", png)
	if err != nil {
		return nil, err
	}
	session, err := xiaohongshu.SessionFingerprint(page)
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(contentOperationTTL)
	s.activateContentOperation(&xhsContentOperation{
		OperationID:        id,
		Kind:               contentOperationDelete,
		NoteID:             request.NoteID,
		ExpectedAccountID:  request.ExpectedAccountID,
		AccountID:          account.UserID,
		TargetFingerprint:  xhsContentFingerprint(target),
		SessionFingerprint: session,
		Current:            target,
		Page:               page,
		ExpiresAt:          expiresAt,
	})

	return &XHSPrepareDeleteResult{
		OperationID:    id,
		Stage:          "ready",
		Target:         target,
		PlatformNotice: modalText,
		Support: XHSContentSupport{
			Video: false, Cover: false, Visibility: false,
			Note: "只删除当前笔记；小红书确认框明确提示删除后将无法恢复",
		},
		ExpiresAt:      expiresAt,
		ScreenshotPath: screenshotPath,
		Message:        "小红书删除确认框已打开但尚未点击确定；核对目标后调用 confirm_content_delete",
	}, nil
}

func (s *XiaohongshuService) ConfirmContentDelete(
	ctx context.Context,
	operationID string,
) (*XHSConfirmContentResult, error) {
	s.contentMu.Lock()
	defer s.contentMu.Unlock()

	if receipt, found, err := s.readContentReceipt(operationID); err != nil {
		return nil, err
	} else if found {
		return xhsContentReceiptResult(receipt)
	}
	operation, err := s.requireActiveContentOperation(operationID, contentOperationDelete)
	if err != nil {
		return nil, err
	}
	if operation.Attempted {
		return nil, fmt.Errorf("该操作已经尝试删除，程序不会再次点击")
	}
	account, err := s.currentContentAccount(ctx)
	if err != nil {
		return nil, err
	}
	target, err := s.locateContentTarget(ctx, operation.NoteID, account)
	if err != nil {
		return nil, err
	}
	session, err := xiaohongshu.SessionFingerprint(operation.Page)
	if err != nil {
		return nil, err
	}
	if err := operation.validate(time.Now(), account.UserID, xhsContentFingerprint(target), session); err != nil {
		return nil, err
	}
	action := xiaohongshu.NewContentAction(operation.Page)
	modalText, err := action.ContentDialogText("删除")
	if err != nil {
		return nil, err
	}
	if !strings.Contains(modalText, operation.Current.Title) {
		return nil, fmt.Errorf("删除确认框中的作品与预览目标不一致，拒绝删除")
	}

	operation.Attempted = true
	submitting := xhsContentReceipt{
		OperationID: operation.OperationID,
		Kind:        contentOperationDelete,
		NoteID:      operation.NoteID,
		AccountID:   operation.AccountID,
		Stage:       contentReceiptSubmitting,
		Message:     "已开始删除小红书笔记，尚未确认结果",
	}
	if err := s.writeContentReceipt(submitting); err != nil {
		operation.Attempted = false
		return nil, err
	}
	if err := action.ConfirmDeletion(ctx); err != nil {
		return s.recordUnknownContentOperation(submitting, err)
	}
	missing, err := action.NoteMissing(ctx, operation.NoteID)
	if err != nil || !missing {
		if err == nil {
			err = fmt.Errorf("删除后笔记仍在管理列表中")
		}
		return s.recordUnknownContentOperation(submitting, err)
	}
	now := time.Now()
	receipt := xhsContentReceipt{
		OperationID: operation.OperationID,
		Kind:        contentOperationDelete,
		NoteID:      operation.NoteID,
		AccountID:   operation.AccountID,
		Stage:       contentReceiptSubmitted,
		Submitted:   now,
		Message:     "小红书笔记已从管理列表移除",
	}
	if err := s.writeContentReceipt(receipt); err != nil {
		return nil, fmt.Errorf("删除已确认，但保存回执失败: %w", err)
	}
	return xhsContentReceiptResult(receipt)
}

func waitXHSEditMutation(ctx context.Context, page *rod.Page) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		body, err := page.Element("body")
		if err == nil {
			text, textErr := body.Text()
			if textErr == nil {
				for _, marker := range []string{"发布成功", "修改成功", "更新成功", "保存成功"} {
					if strings.Contains(strings.ReplaceAll(text, " ", ""), marker) {
						return nil
					}
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("已点击提交，但页面未返回可验证的小红书编辑结果；请检查创作者中心，禁止自动重试")
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
