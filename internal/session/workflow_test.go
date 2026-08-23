package session

import (
	"context"
	"testing"
	"time"
)

type fakeLoginExecutor struct{}

func (fakeLoginExecutor) Run(context.Context, string) (Data, error) {
	return Data{UserAgent: "ua", Cookies: []Cookie{{Name: "sid", Value: "x"}}}, nil
}

func TestWorkflowAllowlistAndSignals(t *testing.T) {
	workflow := Workflow{Executor: fakeLoginExecutor{}, AllowedHosts: map[string]struct{}{"site.example": {}}, Timeout: time.Second}
	if !workflow.Allowed("https://site.example/pricing") || workflow.Allowed("http://site.example/pricing") || workflow.Allowed("https://evil.example/") {
		t.Fatal("allowlist policy failed")
	}
	data, err := workflow.Recover(context.Background(), "https://site.example/pricing")
	if err != nil || len(data.Cookies) != 1 {
		t.Fatalf("recover: %+v %v", data, err)
	}
	signals := ScriptSignals{}
	if !signals.HasChallenge("Just a Moment... Cloudflare") || !signals.HasLinuxDoLogin("使用 Linux DO 登录") || !signals.IsAuthorizationPage("https://connect.linux.do/oauth2/authorize", "允许") {
		t.Fatal("script signal detection failed")
	}
}
