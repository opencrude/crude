package tools

import (
	"strings"
	"testing"
)

func TestReadFileAdvertisesItsCompleteMultimodalContractAcrossAdapters(t *testing.T) {
	for _, model := range []string{"gpt-6.1-sol", "claude-test", "gemini-test", "ollama/local-model"} {
		found := false
		for _, tool := range ForModel(WithModelCommands(AgentTools, model), model) {
			if tool.Function.Name != "read_file" {
				continue
			}
			found = true
			for _, capability := range []string{"line numbers", "offset", "limit", "encoded=true", "Markdown", "DOCX", "PPTX", "XLSX/XLS", "EPUB", "IPYNB", "Images/screenshots", "PDF", "audio", "video", "native attachments", "active model", "provider protocol", "MIME type", "whole-file", "explicit refusal", "directory listing"} {
				if !strings.Contains(tool.Function.Description, capability) {
					t.Fatalf("%s read_file contract omits %q", model, capability)
				}
			}
		}
		if !found {
			t.Fatalf("%s omitted read_file", model)
		}
	}
}
