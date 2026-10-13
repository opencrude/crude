package viewmodel

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/clipboard"
	"crude/provider"
)

// Exercise the actual shell -> framed client -> core store -> provider route,
// not a local ReferenceStore stand-in. The two platform paste gestures and the
// inline/chunked transport boundaries must preserve the same binary image.
func TestConnectedClipboardImageReachesProviderExactly(t *testing.T) {
	for _, size := range []int{2, 1100} {
		t.Run(fmt.Sprint(size), func(t *testing.T) { connectedClipboardJourney(t, size, false) })
	}
}

// Run only with a synthetic system clipboard installed by the acceptance driver.
func TestConnectedNativeClipboardImage(t *testing.T) {
	if os.Getenv("CRUDE_SYNTHETIC_CLIPBOARD") != "1" {
		t.Skip("requires an owned synthetic system clipboard")
	}
	connectedClipboardJourney(t, 2, true)
}

func connectedClipboardJourney(t *testing.T, size int, native bool) {
	var encoded bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.NoCompression}
	if err := encoder.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, size, size))); err != nil {
		t.Fatal(err)
	}
	data := encoded.Bytes()
	if native {
		data = clipboard.Read(clipboard.FmtImage)
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width != 2 || cfg.Height != 2 {
			t.Fatalf("synthetic native clipboard capture failed: %v", err)
		}
	}
	requests := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"clipboard accepted\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	provider.SetModelCap("test-model", true, true)
	runtime := remoteJourneyRuntime(t, server.URL)
	shell := newRemoteJourneyShell(t, runtime, t.TempDir())
	remoteJourneyCreate(t, shell)
	app := shell.app
	app.Input.SetInput("inspect screenshot ")
	app.setFocusInput()
	restore := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		read: func(format clipboard.Format) []byte {
			if format != clipboard.FmtImage {
				t.Error("read unrelated clipboard text")
			}
			if native {
				return clipboard.Read(format)
			}
			return data
		}, writeText: func(string) error { return nil },
	})
	defer restore()
	var gesture tea.Msg = tea.PasteMsg{Content: ""}
	if size == 2 {
		gesture = tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}
	}
	_, cmd := app.Update(gesture)
	runAppCommand(t, app, cmd)
	refs := app.ActiveSession.RefStore.ListReferences()
	if len(refs) != 1 {
		t.Fatalf("clipboard refs = %+v", refs)
	}
	stored, err := os.ReadFile(refs[0].FilePath)
	if err != nil || !bytes.Equal(stored, data) {
		t.Fatalf("core stored encoded/corrupt image: err=%v", err)
	}
	if _, err := png.Decode(bytes.NewReader(stored)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(app.Input.Value(), refs[0].Label) {
		t.Fatal("missing composer chip")
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return strings.Contains(app.View().Content, "clipboard accepted") })
	request := <-requests
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	if bytes.Count(request, []byte(want)) != 1 {
		t.Fatal("provider did not receive the exact image once")
	}
}
