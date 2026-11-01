package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateXHSEditRequest(t *testing.T) {
	title := " 新标题 "
	content := " 新正文 "
	got, err := validateXHSEditRequest(XHSEditRequest{
		NoteID:            " 6ae74c03000000000e00e176 ",
		ExpectedAccountID: " 68343f6e000000001b01ade6 ",
		Title:             &title,
		Content:           &content,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.NoteID != "6ae74c03000000000e00e176" ||
		got.ExpectedAccountID != "68343f6e000000001b01ade6" ||
		got.Title == nil || *got.Title != "新标题" ||
		got.Content == nil || *got.Content != "新正文" {
		t.Fatalf("unexpected normalized request: %+v", got)
	}

	empty := ""
	cases := []XHSEditRequest{
		{ExpectedAccountID: "account", Title: &title},
		{NoteID: "note", Title: &title},
		{NoteID: "note", ExpectedAccountID: "account"},
		{NoteID: "note", ExpectedAccountID: "account", Title: &empty},
		{NoteID: "note", ExpectedAccountID: "account", Title: stringPointer(strings.Repeat("字", 21))},
		{NoteID: "note", ExpectedAccountID: "account", Content: stringPointer(strings.Repeat("字", 1001))},
	}
	for _, request := range cases {
		if _, err := validateXHSEditRequest(request); err == nil {
			t.Fatalf("invalid request accepted: %+v", request)
		}
	}
}

func TestXHSContentOperationStateChecks(t *testing.T) {
	now := time.Now()
	operation := &xhsContentOperation{
		OperationID:        "op-1",
		Kind:               contentOperationDelete,
		NoteID:             "6ae74c03000000000e00e176",
		ExpectedAccountID:  "68343f6e000000001b01ade6",
		AccountID:          "68343f6e000000001b01ade6",
		TargetFingerprint:  "content",
		SessionFingerprint: "session",
		ExpiresAt:          now.Add(time.Minute),
		Active:             true,
	}
	if err := operation.validate(now, operation.AccountID, operation.TargetFingerprint, operation.SessionFingerprint); err != nil {
		t.Fatalf("fresh operation rejected: %v", err)
	}
	for name, check := range map[string]func() error{
		"expired": func() error {
			return operation.validate(operation.ExpiresAt.Add(time.Second), operation.AccountID, operation.TargetFingerprint, operation.SessionFingerprint)
		},
		"account": func() error {
			return operation.validate(now, "other", operation.TargetFingerprint, operation.SessionFingerprint)
		},
		"target": func() error {
			return operation.validate(now, operation.AccountID, "other", operation.SessionFingerprint)
		},
		"session": func() error {
			return operation.validate(now, operation.AccountID, operation.TargetFingerprint, "other")
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := check(); err == nil {
				t.Fatal("stale operation accepted")
			}
		})
	}
}

func TestXHSContentReceiptRoundTrip(t *testing.T) {
	service := &XiaohongshuService{contentDataDir: t.TempDir()}
	receipt := xhsContentReceipt{
		OperationID: "op-1",
		Kind:        contentOperationDelete,
		NoteID:      "6ae74c03000000000e00e176",
		Stage:       contentReceiptSubmitted,
		Message:     "已删除",
	}
	if err := service.writeContentReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	got, found, err := service.readContentReceipt(receipt.OperationID)
	if err != nil || !found || got.NoteID != receipt.NoteID {
		t.Fatalf("unexpected receipt: %+v found=%v err=%v", got, found, err)
	}
	path := filepath.Join(service.contentDataDir, "content-receipts", receipt.OperationID+".json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.readContentReceipt(receipt.OperationID); err == nil {
		t.Fatal("corrupt receipt accepted")
	}
}

func TestXHSContentReceiptUnknownRequiresManualCheck(t *testing.T) {
	_, err := xhsContentReceiptResult(xhsContentReceipt{
		OperationID: "op-1",
		Kind:        contentOperationDelete,
		NoteID:      "6ae74c03000000000e00e176",
		Stage:       contentReceiptUnknown,
		Message:     "结果未知",
	})
	if err == nil || !strings.Contains(err.Error(), "不要自动重试") {
		t.Fatalf("unexpected unknown-result handling: %v", err)
	}
}

func TestActivateXHSContentOperationInvalidatesPrevious(t *testing.T) {
	service := &XiaohongshuService{}
	first := &xhsContentOperation{OperationID: "first"}
	second := &xhsContentOperation{OperationID: "second"}

	service.activateContentOperation(first)
	service.activateContentOperation(second)

	if service.activeContentOperation != second {
		t.Fatal("new operation was not activated")
	}
	if first.Active {
		t.Fatal("previous operation remained active")
	}
}

func TestXHSContentFingerprintDetectsMetadataChanges(t *testing.T) {
	base := XHSContentTarget{
		NoteID:      "6ae74c03000000000e00e176",
		Title:       "把想念寄出去",
		Content:     "旧版正文",
		PublishedAt: "2026-11-01 22:26",
		AccountID:   "68343f6e000000001b01ade6",
	}
	fingerprint := xhsContentFingerprint(base)
	if fingerprint == "" {
		t.Fatal("empty fingerprint")
	}
	changed := base
	changed.Content = "已经变化的正文"
	if fingerprint == xhsContentFingerprint(changed) {
		t.Fatal("content change did not alter fingerprint")
	}
}

func TestValidateXHSDeleteRequest(t *testing.T) {
	got, err := validateXHSDeleteRequest(XHSDeleteRequest{
		NoteID:            " 6ae74c03000000000e00e176 ",
		ExpectedAccountID: " 68343f6e000000001b01ade6 ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.NoteID != "6ae74c03000000000e00e176" ||
		got.ExpectedAccountID != "68343f6e000000001b01ade6" {
		t.Fatalf("unexpected normalized request: %+v", got)
	}
	for _, request := range []XHSDeleteRequest{
		{ExpectedAccountID: "account"},
		{NoteID: "note"},
	} {
		if _, err := validateXHSDeleteRequest(request); err == nil {
			t.Fatalf("invalid request accepted: %+v", request)
		}
	}
}

func stringPointer(value string) *string {
	return &value
}
