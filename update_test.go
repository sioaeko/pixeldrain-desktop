package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.2.0", "1.1.0", true},
		{"1.10.0", "1.9.2", true},
		{"1.1.0", "1.1.0", false},
		{"1.0.9", "1.1.0", false},
		{"2", "1.9.9", true},
		{"1.1", "1.1.0", false},
	}
	for _, c := range cases {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestCheckUpdate(t *testing.T) {
	body := `{"tag_name":"v1.2.0","html_url":"https://example.test/r/v1.2.0","draft":false,"prerelease":false}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("update check must not send credentials")
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	old := updateURL
	updateURL = srv.URL
	defer func() { updateURL = old }()

	info, err := checkUpdate(context.Background(), "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Available || info.Latest != "1.2.0" || info.URL != "https://example.test/r/v1.2.0" {
		t.Fatalf("unexpected %+v", info)
	}
	if info, _ = checkUpdate(context.Background(), "1.2.0"); info.Available {
		t.Fatal("same version reported as an update")
	}
}
