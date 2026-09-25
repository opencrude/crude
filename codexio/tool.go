// Adapted for Crude from fgn/applypatch 95ed66f6d36a575b00001f2e27aedc1ec7455812.
// See NOTICE for provenance and modification details.
// Copyright 2025 OpenAI
// Copyright 2026 Fredrik Gustafsson
// SPDX-License-Identifier: Apache-2.0
//
// The apply_patch tool definition of OpenAI Codex
// (https://github.com/openai/codex, codex-rs/core/src/tools/handlers/
// apply_patch_spec.rs and codex-rs/core/assets/tools/apply_patch.lark,
// copied unchanged into apply_patch.lark). See ../LICENSE-APACHE and NOTICE.

package codexio

import _ "embed"

//go:embed apply_patch.lark
var codexGrammar string

// Tool is a custom (freeform) tool definition for the OpenAI Responses API.
type Tool struct {
	Type        string     `json:"type"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Format      ToolFormat `json:"format"`
}

// ToolFormat constrains the input of a custom tool.
type ToolFormat struct {
	Type       string `json:"type"`
	Syntax     string `json:"syntax"`
	Definition string `json:"definition"`
}

// CodexTool returns the apply_patch tool that Codex gives GPT-5 models: a
// custom tool whose input must match a Lark grammar for the
// "*** Begin Patch" format. Pass the input of the model's custom_tool_call
// to Mode.ApplyFiles.
func CodexTool() Tool {
	return Tool{
		Type: "custom",
		Name: "apply_patch",
		Description: "The `apply_patch` tool can be used to edit files. " +
			"This is a FREEFORM tool, so do not wrap the patch in JSON.",
		Format: ToolFormat{Type: "grammar", Syntax: "lark", Definition: codexGrammar},
	}
}
