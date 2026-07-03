package viewmodel

import "crude/tui/workspace"

type AgentState = workspace.AgentState
type AgentManager = workspace.AgentManager
type AgentNode = workspace.AgentNode

var NewAgentState = workspace.NewAgentState
var NewAgentManager = workspace.NewAgentManager

type AgentDoneResultMsg = workspace.AgentDoneResultMsg
type AgentErrorResultMsg = workspace.AgentErrorResultMsg
type AgentRetryResultMsg = workspace.AgentRetryResultMsg

type agentDoneResultMsg = workspace.AgentDoneResultMsg
type agentErrorResultMsg = workspace.AgentErrorResultMsg
type agentRetryResultMsg = workspace.AgentRetryResultMsg

var FormatErr = workspace.FormatErr

var TickCmd = workspace.TickCmd
var StreamingTickCmd = workspace.StreamingTickCmd
