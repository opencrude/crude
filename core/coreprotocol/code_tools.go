package coreprotocol

import (
	"crude/types/insight"
)

const ReadCodeToolInventory = "code_tools.inventory"
const PreviewCodeToolRemoval = "code_tools.remove_preview"
const RemoveCodeTools = "code_tools.remove"

type ReadCodeToolInventoryRequest struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

type ReadCodeToolInventoryResult struct {
	Inventory insight.CodeToolInventory `json:"inventory"`
}

type PreviewCodeToolRemovalRequest struct {
	IDs    []string `json:"ids"`
	Unused bool     `json:"unused"`
}

type PreviewCodeToolRemovalResult struct {
	Preview insight.CodeToolRemovalPreview `json:"preview"`
}

type RemoveCodeToolsRequest struct {
	Preview insight.CodeToolRemovalPreview `json:"preview"`
}

type RemoveCodeToolsResult struct{}
