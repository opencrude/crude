package viewmodel

import (
	"database/sql"
	"os"
	"time"

	"crude/sessiondb"
	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/input"
	"crude/tui/rendering"
	"crude/tui/settings"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/widgets"
	"crude/tui/window"
)

type Workspace struct {
	theme        *theme.Theme
	overlayState *OverlayState
	agentMgr     *AgentManager
	paneMgr      *PaneManager
	input        input.Model
	statusbar    *widgets.StatusBar
	settings     settings.Model
	confirm      widgets.ConfirmDialog
	questionForm widgets.QuestionForm
	yoloDlg      widgets.YoloDialog
	textOverlay  widgets.TextOverlay
	filePicker   widgets.FilePicker
	refStore     *referenceStore

	win     *window.Window
	overlay *window.Window

	tree        *TreeState
	focusStack  *FocusStack
	editorState *EditorState

	OnNewSession        func() *NewSessionResult
	OnSettingsSave      func(ttypes.Settings) error
	OnConfirmResult     func(ttypes.ConfirmResult)
	OnQuestionResult    func(ttypes.QuestionResult)
	OnResumeSession     func(string)
	OnCancelSubAgent    func(agentID string)
	OnHardCancel        func()
	OnOrchestratorReset func(orchID string)
	FetchModelsFn       func(string, string, string) ([]ttypes.ModelEntry, error)
	GetProviderConfigFn func(string) ttypes.ProviderConfig
	ProviderPresets     []ttypes.ProviderPreset

	modelName      string
	username       string
	startTime      time.Time
	chatAreaHeight int

	spinFrame int
	history   []string

	tokenPrompt, tokenCompletion, tokenCached int
	sessionCost                               float64
	contextWindow, contextUsed                int
	tokenEstimated                            bool

	lastSettings ttypes.Settings

	formHint string

	logger Logger

	launcher            *LauncherState
	onDeleteSession     func(string)
	onListSessions      func() []sessiondb.SessionMeta
	lastInteractionTime time.Time

	commandPaletteModel *commandPaletteModel

	diagPopupID string

	lspManager *LSPManager

	composer *widgets.Composer

	lspPopup *LSPPopupState

	dirtyPromptPath   string
	dirtyPromptChoice int

	findReplace *widgets.FindReplaceState

	lastContextMenu     *widgets.ContextMenuState
	contextMenuOpenedAt time.Time

	treePrompt       *widgets.TextPromptState
	treeDeletePending string
}

func NewWorkspace(modelName string, username string, initial ttypes.Settings, onSave func(ttypes.Settings) error, getProviderConfigFn func(string) ttypes.ProviderConfig, lg Logger, sessions []sessiondb.SessionMeta, sessionID, sessionDir string, db *sql.DB) *Workspace {
	th := theme.DefaultDark()
	im := input.NewModel(th)
	sb := widgets.NewStatusBar(th)
	sm := settings.NewModel(th, initial, nil, getProviderConfigFn, nil)
	cd := widgets.NewConfirmDialog(th)
	qd := widgets.NewQuestionForm(th)
	yd := widgets.NewYoloDialog(th)
	to := widgets.NewTextOverlay(th)
	fp := widgets.NewFilePicker(th)
	refs := newReferenceStore(sessionID, sessionDir, db)

	rendering.SetInvalidUTF8Logger(func(tag, msg string, pairs ...any) { lg.Warn(tag, msg, pairs...) })

	pm := NewPaneManager()

	w := window.New(th)
	ov := window.New(th)

	wsDir, _ := os.Getwd()
	if wsDir == "" {
		wsDir = sessionDir
	}
	treeModel := filetree.NewFileTreeModel(wsDir)
	treeRenderer := filetree.NewTreeRenderer(treeModel, treeModel.IconResolver())
	treeRenderer.ApplyTheme(th)

	launcher := NewLauncherState(sessions)

	ed := editor.New(th)
	lspMgr := NewLSPManager(lg)
	composerWidget := widgets.NewComposer(th)

	ws := &Workspace{
		theme:           th,
		overlayState:    NewOverlayState(),
		agentMgr:        NewAgentManager(),
		paneMgr:         pm,
		win:             w,
		overlay:         ov,
		composer:        composerWidget,
		input:           im,
		statusbar:       sb,
		settings:        sm,
		confirm:         cd,
		questionForm:    qd,
		yoloDlg:         yd,
		textOverlay:     to,
		filePicker:      fp,
		refStore:        refs,
		editorState:     NewEditorState(),
		modelName:       modelName,
		username:        username,
		startTime:       time.Now(),
		lastSettings:    initial,
		OnSettingsSave:  onSave,
		logger:          lg,
		launcher:        launcher,
		lspManager:      lspMgr,
		lspPopup:        NewLSPPopupState(),
		tree:            NewTreeState(treeModel, treeRenderer, th.Tree.Config.DefaultWidth),
		focusStack:      NewFocusStack(),
	}

	ws.editorState.Editor = ed
	ws.input.SetReferenceStore(refs)

	return ws
}
