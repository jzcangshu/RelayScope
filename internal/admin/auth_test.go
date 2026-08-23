package admin

import "testing"

func TestAuthLoginAndExpiry(t *testing.T) {
	t.Parallel()
	auth, err := NewAuth("this-is-a-long-test-password")
	if err != nil {
		t.Fatal(err)
	}
	if token, ok := auth.Login("127.0.0.1", "wrong"); ok || token != "" {
		t.Fatal("wrong password accepted")
	}
	token, ok := auth.Login("127.0.0.1", "this-is-a-long-test-password")
	if !ok || token == "" || !auth.Valid(token) {
		t.Fatal("valid login failed")
	}
	csrf, ok := auth.NewCSRFToken(token)
	if !ok || csrf == "" || csrf == token {
		t.Fatal("expected independent CSRF token")
	}
	if got, valid := auth.CSRFToken(token); !valid || got != csrf {
		t.Fatal("expected CSRF token lookup")
	}
}
