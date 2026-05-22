package main

import (
	"flag"
	"fmt"
	"os"

	"crude/app"
	"crude/session"
	"crude/tui/platform"
)

func main() {
	platform.InitConsole()

	apiKeyFlag := flag.String("key", "", "API key (or set CRUDE_API_KEY)")
	modelFlag := flag.String("model", "", "Model to use")
	providerFlag := flag.String("provider", "", "Provider ID (openrouter, openai, anthropic, etc.)")
	sysPromptFlag := flag.String("system", "", "Override system prompt")
	settingsFlag := flag.Bool("settings", false, "Open settings on startup")
	resumeFlag := flag.String("resume", "", "Resume session (session ID, 'last', or empty for picker)")
	listSessionsFlag := flag.Bool("list-sessions", false, "List sessions and exit")
	limitFlag := flag.Int("limit", 50, "Number of sessions to list (used with --list-sessions)")
	offsetFlag := flag.Int("offset", 0, "Offset for session listing (used with --list-sessions)")
	deleteSessionFlag := flag.String("delete-session", "", "Delete a session by ID")
	flag.Parse()

	sessionsRoot, _ := session.SessionsRoot()

	if *listSessionsFlag {
		app.ListSessions(sessionsRoot, *limitFlag, *offsetFlag)
		return
	}

	if *deleteSessionFlag != "" {
		app.DeleteSession(sessionsRoot, *deleteSessionFlag)
		return
	}

	resumeID, resumeErr := app.ResolveResumeID(sessionsRoot, *resumeFlag)
	if resumeErr != nil {
		fmt.Fprintln(os.Stderr, resumeErr.Error())
		os.Exit(1)
	}
	isResuming := resumeID != ""

	fc := app.LoadFileConfig()
	if fc.Providers == nil {
		fc.Providers = make(map[string]session.ProviderConfig)
	}

	providerID := fc.ActiveProvider
	if providerID == "" {
		providerID = "openrouter"
		fc.ActiveProvider = providerID
	}
	if *providerFlag != "" {
		providerID = *providerFlag
		fc.ActiveProvider = providerID
	}

	pc := fc.Providers[providerID]
	if pc.APIKey == "" {
		if *apiKeyFlag != "" {
			pc.APIKey = *apiKeyFlag
		} else {
			pc.APIKey = os.Getenv("CRUDE_API_KEY")
			if pc.APIKey == "" {
				pc.APIKey = os.Getenv("WAFER_API_KEY")
			}
			if pc.APIKey == "" {
				pc.APIKey = os.Getenv("OPENROUTER_API_KEY")
			}
		}
	}
	fc.Providers[providerID] = pc

	if *modelFlag != "" {
		if fc.Agents == nil {
			fc.Agents = &session.AgentsConfig{ActiveProfile: "default", Profiles: make(map[string]session.ProfileConfig)}
		}
		ap := fc.Agents.ActiveProfile
		if ap == "" {
			ap = "default"
		}
		if fc.Agents.Profiles == nil {
			fc.Agents.Profiles = make(map[string]session.ProfileConfig)
		}
		prof := fc.Agents.Profiles[ap]
		if prof.Categories == nil {
			prof.Categories = make(map[string]session.CategoryConfig)
		}
		mainCat := prof.Categories[session.CategoryMain]
		mainCat.Model = *modelFlag
		mainCat.Provider = providerID
		prof.Categories[session.CategoryMain] = mainCat
		fc.Agents.Profiles[ap] = prof
	}

	_ = sysPromptFlag

	sessCfg := app.FileConfigToSession(fc)
	home, _ := os.UserHomeDir()

	application, err := app.New(app.Opts{
		FC:              fc,
		SessCfg:         sessCfg,
		Home:            home,
		SessionsRoot:    sessionsRoot,
		ResumeSessionID: resumeID,
		IsResuming:      isResuming,
		OpenSettings:    *settingsFlag,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	application.Run()
}
