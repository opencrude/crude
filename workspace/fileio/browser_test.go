package fileio

import "testing"

func TestConfirmedURLsDoNotRelaxAutomaticBrowserPolicy(t *testing.T) {
	if _, err := WebURL("http://example.test/docs"); err != nil {
		t.Fatal(err)
	}
	if err := OpenBrowserURL(t.Context(), "http://example.test/docs"); err == nil {
		t.Fatal("unconfirmed browser call accepted a non-loopback HTTP destination")
	}
	for _, s := range []string{"file:///a", "javascript:alert(1)", "https://a@b.example/", "https://:443/"} {
		if _, err := WebURL(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
