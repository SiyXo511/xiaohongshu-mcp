package main

import (
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
