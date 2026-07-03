package viewmodel

// OverlayState manages the current overlay mode and related state.
// This consolidates the Mode enum, blocking/yolo flags, and blockedSessionID
// into one place instead of having them as loose App fields.
type OverlayState struct {
	Mode             Mode
	Blocking         bool
	Yolo             bool
	BlockedSessionID string
	SettingsCategory string
}

func NewOverlayState() *OverlayState {
	return &OverlayState{
		Mode: ModeChat,
	}
}

func (o *OverlayState) Set(m Mode)                        { o.Mode = m }
func (o *OverlayState) Is(m Mode) bool                    { return o.Mode == m }
func (o *OverlayState) Current() Mode                     { return o.Mode }
func (o *OverlayState) IsChat() bool                      { return o.Mode == ModeChat }
func (o *OverlayState) IsOverlay() bool                   { return o.Mode != ModeChat }
func (o *OverlayState) Name() string                      { return modeName(o.Mode) }
func (o *OverlayState) SetBlocking(v bool)                { o.Blocking = v }
func (o *OverlayState) IsBlocking() bool                  { return o.Blocking }
func (o *OverlayState) SetYolo(v bool)                    { o.Yolo = v }
func (o *OverlayState) IsYolo() bool                      { return o.Yolo }
func (o *OverlayState) SetBlockedSessionID(id string)     { o.BlockedSessionID = id }
func (o *OverlayState) GetBlockedSessionID() string       { return o.BlockedSessionID }
func (o *OverlayState) SetSettingsCategory(cat string)    { o.SettingsCategory = cat }
func (o *OverlayState) GetSettingsCategory() string       { return o.SettingsCategory }
