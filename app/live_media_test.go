package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"crude/agent"
	"crude/logger"
	"crude/provider"
)

func TestLiveCommandCodeToolResultImageSemantics(t *testing.T) {
	if os.Getenv("CRUDE_LIVE_MEDIA") != "1" {
		t.Skip("CRUDE_LIVE_MEDIA=1 not set")
	}
	fileConfig := LoadFileConfig()
	providerConfig, ok := fileConfig.Providers["commandcode"]
	accountID := ""
	if fileConfig.Agents != nil {
		profile := fileConfig.Agents.Profiles[fileConfig.Agents.ActiveProfile]
		accountID = profile.Categories["default"].AccountID
	}
	account, accountOK := fileConfig.Accounts[accountID]
	if !ok || !accountOK || account.ProviderID != "commandcode" || account.APIKey == "" {
		t.Skip("Command Code credential is not configured")
	}
	model := os.Getenv("CRUDE_LIVE_MEDIA_MODEL")
	standard := providerConfig.APIStandard
	if fileConfig.Agents != nil {
		profile := fileConfig.Agents.Profiles[fileConfig.Agents.ActiveProfile]
		category := profile.Categories["default"]
		if model == "" {
			model = category.Model
		}
	}
	if model == "" {
		t.Skip("main Command Code model is not configured")
	}
	if standard == "" {
		standard = "openai"
	}

	template := provider.ResolveTemplate("commandcode", providerConfig, account.Overrides)
	p := provider.FromPreset("commandcode", account.APIKey, template.ProviderURL)
	if err := template.Apply(p); err != nil {
		t.Fatal(err)
	}
	p.Model = model
	if err := p.SetStandardID(standard); err != nil {
		t.Fatal(err)
	}
	lg, err := logger.New(logger.Options{HomeDir: t.TempDir(), SessionID: "live-media"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lg.Close() }()
	a := agent.NewAgent(p, model, lg)
	a.MaxRetries = 1

	imageURL := liveMediaImageURL(t)
	messages := []provider.ChatMessage{
		provider.NewTextMessage("user", "Inspect the image returned by read_file and report its layout and colors."),
		{
			Role: "assistant",
			ToolCalls: []provider.ToolCall{{
				ID: "call_live_image", Type: "function",
				Function: provider.FunctionCall{Name: "read_file", Arguments: `{"path":"generated-live-probe.png"}`},
			}},
		},
		{
			Role: "tool", ToolCallID: "call_live_image",
			Content: provider.PartsContent([]provider.ContentPart{
				{Type: "text", Text: "generated-live-probe.png read as image attachment"},
				{Type: "image_url", ImageURL: &provider.ImageURL{URL: imageURL}},
			}),
		},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	result := a.Run(ctx, messages,
		"Analyze the supplied image directly. It has four solid quadrants. Name the top-left, top-right, bottom-left, and bottom-right colors. Do not call tools.",
		func(agent.AgentEvent) {}, nil, "", nil)
	if ctx.Err() != nil {
		t.Fatalf("live media request timed out: %v", ctx.Err())
	}
	var answer string
	for _, message := range result {
		if message.Role == "assistant" && message.Content.String() != "" {
			answer = message.Content.String()
		}
	}
	lower := strings.ToLower(answer)
	for _, required := range []string{"red", "green", "blue", "yellow"} {
		if !strings.Contains(lower, required) {
			t.Fatalf("live model did not inspect image; missing %q in %q", required, answer)
		}
	}
}

func liveMediaImageURL(t *testing.T) string {
	t.Helper()
	const size = 128
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var pixel color.RGBA
			switch {
			case x < size/2 && y < size/2:
				pixel = color.RGBA{R: 255, A: 255}
			case x >= size/2 && y < size/2:
				pixel = color.RGBA{G: 255, A: 255}
			case x < size/2 && y >= size/2:
				pixel = color.RGBA{B: 255, A: 255}
			default:
				pixel = color.RGBA{R: 255, G: 255, A: 255}
			}
			img.SetRGBA(x, y, pixel)
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
}
