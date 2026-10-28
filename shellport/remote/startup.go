package remote

import (
	"context"
	"sync"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
	"crude/workspace"
)

// StartupClient shares the selected core's connection without attaching a root.
// Its caller owns closing the connection after selection or quit.
type StartupClient struct {
	Client   *coreclient.Client
	Dial     Dialer
	ClientID string
	mu       sync.Mutex
}

var _ shellport.Startup = (*StartupClient)(nil)

func (s *StartupClient) call(ctx context.Context, method string, in coreprotocol.Value, out coreprotocol.Target) error {
	s.mu.Lock()
	if s.Client == nil || s.Client.Err() != nil {
		if s.Dial == nil {
			s.mu.Unlock()
			return coreclient.ErrClosed
		}
		if s.Client != nil {
			_ = s.Client.Close()
		}
		client, err := s.Dial(ctx, s.ClientID)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		s.Client = client
	}
	client := s.Client
	s.mu.Unlock()
	// An uncertain Save is never replayed here. The next explicit action reads
	// committed state or makes the same idempotent preference choice.
	return client.Call(ctx, "", method, in, out)
}

func (s *StartupClient) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Client != nil {
		client := s.Client
		s.Client = nil
		return client.Close()
	}
	return nil
}

func (s *StartupClient) Inspect(ctx context.Context, root string) (coreprotocol.StartupSnapshot, error) {
	var out coreprotocol.StartupSnapshot
	err := s.call(ctx, coreprotocol.StartupInspect, coreprotocol.StartupInspectRequest{Root: root}, &out)
	return out, err
}

func (s *StartupClient) Browse(ctx context.Context, in coreprotocol.StartupBrowseRequest) (coreprotocol.StartupBrowseResult, error) {
	var out coreprotocol.StartupBrowseResult
	err := s.call(ctx, coreprotocol.StartupBrowse, in, &out)
	return out, err
}

func (s *StartupClient) Catalog(ctx context.Context, root string, hidden bool) (workspace.BrowserCatalog, error) {
	var out workspace.BrowserCatalog
	err := s.call(ctx, coreprotocol.StartupCatalog, coreprotocol.StartupCatalogRequest{Root: root, IncludeHidden: hidden}, &out)
	return out, err
}

func (s *StartupClient) CreateWorkspaceFolder(ctx context.Context, parent, name string) (string, error) {
	var out coreprotocol.StartupCreateFolderResult
	err := s.call(ctx, coreprotocol.StartupCreateFolder, coreprotocol.StartupCreateFolderRequest{Parent: parent, Name: name}, &out)
	return out.Path, err
}

func (s *StartupClient) Save(ctx context.Context, edits []types.SettingEdit) (types.OnboardingSettings, error) {
	var out types.OnboardingSettings
	err := s.call(ctx, coreprotocol.StartupSave, &coreprotocol.StartupSaveRequest{Edits: edits}, &out)
	return out, err
}
