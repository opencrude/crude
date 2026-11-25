package remote

import (
	"context"
	"errors"
	"sync"

	"crude/core/coreclient"
	"crude/core/coreprotocol"
	"crude/core/shellport"
	"crude/internal/retry"
	"crude/types/setting"
	"crude/workspace"
)

// StartupClient shares the selected core's connection without attaching a root.
// Its caller owns closing the connection after selection or quit.
type StartupClient struct {
	Client   *coreclient.Client
	Dial     Dialer
	ClientID string
	options  Options
	mu       sync.Mutex
}

var _ shellport.Startup = (*StartupClient)(nil)

func (s *StartupClient) connection(ctx context.Context) (*coreclient.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.Client == nil || s.Client.Err() != nil {
		if s.Dial == nil {
			return nil, coreclient.ErrClosed
		}
		if s.Client != nil {
			_ = s.Client.Close()
		}
		client, err := s.Dial(ctx, s.ClientID)
		if err != nil {
			return nil, err
		}
		s.Client = client
	}
	return s.Client, nil
}

func (s *StartupClient) call(ctx context.Context, method string, in coreprotocol.Value, out coreprotocol.Target) error {
	client, err := s.connection(ctx)
	if err != nil {
		return err
	}
	err = client.Call(ctx, "", method, in, out)
	if !errors.Is(err, coreclient.ErrSessionLost) {
		return err
	}
	// A navigation request can be the first to discover that the session
	// ended. Read its original path on the selected host once more. Never
	// replay a folder creation or preference save after losing its reply.
	switch method {
	case coreprotocol.StartupInspect, coreprotocol.StartupBrowse, coreprotocol.StartupCatalog:
	default:
		return err
	}
	if err := retry.Wait(ctx, 0); err != nil {
		return err
	}
	client, err = s.connection(ctx)
	if err != nil {
		return err
	}
	return client.Call(ctx, "", method, in, out)
}

// ConnectStartup negotiates the stable identity of the proposed attachment.
// Navigation itself opens no workspace. A different picker destination gets
// its own root-scoped identity; the caller still owns closing navigation.
func ConnectStartup(ctx context.Context, opts Options) (*StartupClient, error) {
	if opts.Dial == nil || opts.Root == "" {
		return nil, errors.New("startup needs a dialer and a project root")
	}
	id := opts.attachmentID()
	c, err := opts.Dial(ctx, id)
	if err != nil {
		return nil, err
	}
	return &StartupClient{Client: c, ClientID: id, Dial: opts.Dial, options: opts}, nil
}

// AttachWorkspace opens root with the user's entry intent. The core decides
// admission; an explicit top-level choice or a file launch only states intent.
func (s *StartupClient) AttachWorkspace(ctx context.Context, root string, entry coreprotocol.EntryIntent) (*Workspace, error) {
	opts := s.options
	opts.Root, opts.Entry = root, entry
	s.mu.Lock()
	var c *coreclient.Client
	if s.Client != nil && s.Client.Err() == nil && root == s.options.Root {
		c, s.Client = s.Client, nil
	}
	s.mu.Unlock()
	return attachWithClient(ctx, opts, c)
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
	return s.InspectLaunch(ctx, coreprotocol.StartupInspectRequest{Root: root})
}

// InspectLaunch resolves the CLI path on the selected core before attachment.
func (s *StartupClient) InspectLaunch(ctx context.Context, input coreprotocol.StartupInspectRequest) (coreprotocol.StartupSnapshot, error) {
	var out coreprotocol.StartupSnapshot
	err := s.call(ctx, coreprotocol.StartupInspect, input, &out)
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

// Save stores the startup edits through the core.
func (s *StartupClient) Save(ctx context.Context, edits []setting.Edit) (setting.Onboarding, error) {
	var out setting.Onboarding
	err := s.call(ctx, coreprotocol.StartupSave, &coreprotocol.StartupSaveRequest{Edits: edits}, &out)
	return out, err
}
