package coreprotocol

import "crude/types"

const ReadCodeToolInventory = "code_tools.inventory"
const PreviewCodeToolRemoval = "code_tools.remove_preview"
const RemoveCodeTools = "code_tools.remove"

type ReadCodeToolInventoryRequest struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

type ReadCodeToolInventoryResult struct {
	Inventory types.CodeToolInventory `json:"inventory"`
}

type PreviewCodeToolRemovalRequest struct {
	IDs    []string `json:"ids"`
	Unused bool     `json:"unused"`
}

type PreviewCodeToolRemovalResult struct {
	Preview types.CodeToolRemovalPreview `json:"preview"`
}

type RemoveCodeToolsRequest struct {
	Preview types.CodeToolRemovalPreview `json:"preview"`
}

type RemoveCodeToolsResult struct{}
