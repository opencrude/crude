//go:build windows

package desktopnotify

import (
	"strings"
	"testing"
)

func TestToastXMLEscapesText(t *testing.T) {
	got, err := toastXML(Notification{Title: `<b>"x"</b>`, Body: "a & b\n<image src='c:/x'/>"})
	if err != nil {
		t.Fatal(err)
	}
	want := `<toast><visual><binding template="ToastGeneric"><text>&lt;b&gt;&#34;x&#34;&lt;/b&gt;</text><text>a &amp; b&#xA;&lt;image src=&#39;c:/x&#39;/&gt;</text></binding></visual></toast>`
	if got != want {
		t.Fatalf("xml =\n%s\nwant\n%s", got, want)
	}
	if got, _ := toastXML(Notification{Title: "only"}); strings.Count(got, "<text>") != 1 || strings.Contains(got, "<audio") {
		t.Fatalf("a title without body has one text and the default sound: %s", got)
	}
	if got, _ := toastXML(Notification{Title: "t", Silent: true}); !strings.HasSuffix(got, `</visual><audio silent="true"></audio></toast>`) {
		t.Fatalf("a silent toast must turn the sound off: %s", got)
	}
}

func TestToastTagIsShortAndStable(t *testing.T) {
	long := strings.Repeat("workspace:C:/a/very/long/path/", 20)
	if tag := toastTag(long); len(tag) > 64 || tag != toastTag(long) || tag == toastTag(long+"x") {
		t.Fatalf("tag %q", tag)
	}
}
