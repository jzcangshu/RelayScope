package notifier

import (
	"strings"
	"testing"
)

func TestChatIDIntent(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"/start", true},
		{"/chatid", true},
		{"/chatid?", true},
		{"chatid", true},
		{"chat id", true},
		{"/chatid 帮我查一下", true},
		{"/start hi", true},
		{" /CHATID ", true},
		// 不应触发
		{"hello", false},
		{"今天天气怎么样", false},
		{"", false},
		{"/cmd", false},
		{"chat", false},
	}
	for _, c := range cases {
		if got := chatIDIntent(c.text); got != c.want {
			t.Errorf("chatIDIntent(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestChatIDReply(t *testing.T) {
	got := chatIDReply(1212406777, "https://watchbot.cfd/")
	if !strings.Contains(got, "1212406777") {
		t.Fatalf("reply missing chat id:\n%s", got)
	}
	if !strings.Contains(got, "https://watchbot.cfd") {
		t.Fatalf("reply missing site url:\n%s", got)
	}
	for _, want := range []string{"复制", "推送目标", "测试推送", "保存渠道"} {
		if !strings.Contains(got, want) {
			t.Fatalf("reply missing guidance %q:\n%s", want, got)
		}
	}
	// 无站点地址时退化为不含 URL 的通用引导
	fallback := chatIDReply(42, "  ")
	if strings.Contains(fallback, "http") {
		t.Fatalf("fallback reply should not contain url:\n%s", fallback)
	}
	if !strings.Contains(fallback, "RelayScope") {
		t.Fatalf("fallback reply missing product name:\n%s", fallback)
	}
}

func TestUnknownTextReply(t *testing.T) {
	got := unknownTextReply()
	if !strings.Contains(got, "/chatid") {
		t.Fatalf("unknown reply should point to /chatid:\n%s", got)
	}
}
