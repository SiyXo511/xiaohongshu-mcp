package xiaohongshu

import "testing"

func TestHasAIContentDeclaration(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "精确匹配", text: "笔记含AI合成内容", want: true},
		{name: "包含额外空白", text: "笔记含AI合成内容\n", want: true},
		{name: "占位文案", text: "添加内容类型声明", want: false},
		{name: "其他声明", text: "内容包含营销广告", want: false},
		{name: "空文本", text: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasAIContentDeclaration(tt.text); got != tt.want {
				t.Fatalf("hasAIContentDeclaration(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
