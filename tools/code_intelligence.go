package tools

import "encoding/json"

// CodeIntelligenceTool is offered only while the session's code intelligence
// is on; the session adds it per step, so it never sits in AgentTools.
var CodeIntelligenceTool = ToolDef{
	Type: "function",
	Function: ToolFuncDef{
		Name: "lsp",
		Description: "Ask the workspace's language servers about source code. " +
			"diagnostics: compiler and linter problems in files after analysis settles (use after edits instead of a full build when one file or package changed). " +
			"definition / references / hover: resolve the identifier named by symbol on a 1-based line. " +
			"symbols: outline of a file's declarations. " +
			"Results use 1-based lines and columns and quote the source line.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"operation":{"type":"string","enum":["diagnostics","definition","references","hover","symbols"]},
				"path":{"type":"string","description":"File to query (workspace-relative or absolute)."},
				"paths":{"type":"array","items":{"type":"string"},"maxItems":10,"description":"diagnostics only: check several files in one call instead of path."},
				"line":{"type":"integer","minimum":1,"description":"1-based line of the identifier (definition, references, hover)."},
				"symbol":{"type":"string","description":"The identifier on that line to query; its first occurrence on the line is used."},
				"column":{"type":"integer","minimum":1,"description":"1-based character column, only when symbol occurs more than once on the line."},
				"severity":{"type":"string","enum":["error","warning","all"],"description":"diagnostics only: lowest severity to report; default warning."}
			},
			"required":["operation"]
		}`),
	},
}
