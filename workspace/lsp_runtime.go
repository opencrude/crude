package workspace

import (
	"context"

	"crude/lspclient"
	model "crude/types"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// defaultLSPRuntime supplies the real subprocess transport without a shell.
// Construction is inert; the manager retains ownership of startup policy.
type defaultLSPRuntime struct{}

// NewLSPRuntime returns the neutral subprocess transport factory. Constructing
// it never launches a server; LSPManager retains startup and shutdown policy.
func NewLSPRuntime() LSPRuntime { return defaultLSPRuntime{} }

type runtimeLSPClient struct {
	*lspclient.LSPIntegration
}

var _ LSPRuntime = defaultLSPRuntime{}
var _ LSPClient = (*runtimeLSPClient)(nil)

func (defaultLSPRuntime) NewClient(spec model.LSPServerSpec, rootURI string, startAdmission func() error) LSPClient {
	return &runtimeLSPClient{LSPIntegration: lspclient.NewLSPIntegration(
		spec.Command, spec.Args, rootURI,
		lspclient.WithServerConfiguration(spec.Environment, spec.Settings, spec.InitializationOptions),
		lspclient.WithConfigurationScopes(spec.ConfigurationScopes),
		lspclient.WithStartAdmission(startAdmission),
	)}
}

// RequestRename adapts the protocol result to the workspace request contract.
func (c *runtimeLSPClient) RequestRename(ctx context.Context, path string, line, col int, name string) (*protocol.WorkspaceEdit, error) {
	return c.LSPIntegration.RequestRename(ctx, path, line, col, name)
}
