package notifier

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildFeishuMarkdownElementsConvertsHeadingsAndQuotes(t *testing.T) {
	md := `# 今日情报内参 | 2026-11-28

> **今日三条主线**：AI 算力景气度回升。

---

## 一、核心突发复盘
### 关键事件
- **事件 A**：详细描述。`

	els := buildFeishuMarkdownElements(md, 3200, true)
	raw, err := json.Marshal(els)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(raw)

	if strings.Contains(out, "## 一、") || strings.Contains(out, "# 今日情报内参") || strings.Contains(out, "### 关键事件") {
		t.Fatalf("expected raw markdown heading hashes to be converted, got: %s", out)
	}
	if strings.Contains(out, "> **今日三条主线**") {
		t.Fatalf("expected raw blockquote '>' to be converted, got: %s", out)
	}
	if !strings.Contains(out, `"tag":"hr"`) {
		t.Fatalf("expected native Feishu hr divider, got: %s", out)
	}
	if !strings.Contains(out, "▍ 一、核心突发复盘") || !strings.Contains(out, "┃") {
		t.Fatalf("expected styled section bar and quote accent, got: %s", out)
	}
}
