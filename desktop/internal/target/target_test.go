package target

import "testing"

func TestDebugAddsOnlyTheDebugQuery(t *testing.T) {
	base, err := Parse("http://127.0.0.1:8787/fixture")
	if err != nil {
		t.Fatal(err)
	}
	got := Debug(base)
	if got.RawQuery != "debug=1" || got.Path != "/fixture" {
		t.Fatalf("debug URL = %q", got.String())
	}
	if base.RawQuery != "" {
		t.Fatalf("Debug mutated its input: %q", base.String())
	}
}

func TestClientCannotLoadRemoteOrCredentialURL(t *testing.T) {
	for _, raw := range []string{
		"https://example.com", "http://192.168.1.2:8787", "http://localhost:8787",
		"http://user:secret@127.0.0.1:8787", "file:///etc/passwd",
		"http://127.0.0.1:8787/#open=secret", "http://127.0.0.1:8787/?token=secret",
	} {
		if _, err := Parse(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, raw := range []string{"http://127.0.0.1:8787/", "http://[::1]:8787/", "http://127.0.0.1:18787/fixture"} {
		if _, err := Parse(raw); err != nil {
			t.Errorf("refused %q: %v", raw, err)
		}
	}
}
