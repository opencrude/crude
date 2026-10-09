package core

import (
	"context"
	"crude/document"
	"crude/workspace"
	"errors"
	"path/filepath"
)

// Legacy language views acquire their own document view, never another
// editor's view. DocumentService alone publishes text to the language server.
type lspViews struct{ docs *workspace.DocumentService }

func newLSPViews(docs *workspace.DocumentService) *lspViews { return &lspViews{docs: docs} }
func lspViewOwner(owner document.Owner) document.Owner {
	owner.ClientID = "lsp:" + owner.ClientID
	return owner
}
func (v *lspViews) Open(owner document.Owner, path string) error {
	if v == nil || v.docs == nil {
		return errors.New("document authority unavailable")
	}
	_, err := v.docs.Open(lspViewOwner(owner), path)
	return err
}
func (v *lspViews) Close(owner document.Owner, root, path string) error {
	if v == nil || v.docs == nil {
		return errors.New("document authority unavailable")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	return v.docs.CloseView(lspViewOwner(owner), filepath.Clean(path))
}
func (v *lspViews) Detach(owner document.Owner) {
	if v != nil && v.docs != nil {
		v.docs.Detach(lspViewOwner(owner))
	}
}
func (v *lspViews) Reconcile(ctx context.Context, root, path string) error {
	if v == nil || v.docs == nil {
		return errors.New("document authority unavailable")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	return v.docs.ReconcileDisk(ctx, filepath.Clean(path))
}
