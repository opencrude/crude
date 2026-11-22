package app

import (
	"fmt"
	"os"
	"strings"

	"crude/types/statuscenter"
)

// StartupPreparation is the non-interactive portion of IDE startup. Operational
// failures are retained as typed repair work while a usable shell configuration
// continues in memory; this function never writes config.json.
type StartupPreparation struct {
	Config   FileConfig
	ResumeID string
	Issues   []statuscenter.OperationIssue
}

func PrepareStartup(flags CLIFlags) StartupPreparation {
	path := ConfigPath()
	fc, loadErr := loadFileConfigFrom(path)
	issues := make([]statuscenter.OperationIssue, 0, 2)
	if loadErr != nil {
		fc = DefaultFileConfig()
		if !os.IsNotExist(loadErr) {
			issues = append(issues, configStartupIssue(path, loadErr))
		}
	}

	resolved, resolveErr := resolveConfigFrom(fc, flags)
	fc = resolved
	if resolveErr != nil {
		issues = append(issues, selectionStartupIssue(resolveErr, flags))
	}

	resumeID, resumeErr := ResolveResume(flags)
	if resumeErr != nil {
		issues = append(issues, resumeStartupIssue(flags.Resume, resumeErr))
	} else {
		return StartupPreparation{Config: fc, ResumeID: resumeID, Issues: issues}
	}
	return StartupPreparation{Config: fc, Issues: issues}
}

func configStartupIssue(path string, err error) statuscenter.OperationIssue {
	return statuscenter.OperationIssue{
		Title: "Configuration needs repair", Summary: "Crude could not read the saved application configuration.",
		Detail: err.Error(), Guidance: "Open the configuration file, correct the reported syntax or access problem, then restart Crude.",
		Target: statuscenter.RepairConfigFile, Path: path,
	}
}

func selectionStartupIssue(err error, flags CLIFlags) statuscenter.OperationIssue {
	target := statuscenter.RepairMainAgent
	if flags.Account != "" || strings.Contains(err.Error(), "account") || strings.Contains(err.Error(), "credentials") {
		target = statuscenter.RepairAccounts
	}
	return statuscenter.OperationIssue{Title: "Account setup required", Summary: "The selected model connection is not available.", Detail: err.Error(), Guidance: "Review the account and Main Agent selections; Crude did not choose a replacement account.", Target: target}
}

func networkStartupIssue(err error) statuscenter.OperationIssue {
	return statuscenter.OperationIssue{Title: "Network settings need repair", Summary: "Outbound network access is blocked because the saved policy is invalid.", Detail: err.Error(), Guidance: "Review Network settings and save a valid proxy, timeout, and certificate policy. Crude will not bypass it with a direct connection.", Target: statuscenter.RepairNetwork}
}

func remoteListenerIssue(err error) statuscenter.OperationIssue {
	return statuscenter.OperationIssue{Title: "Remote access needs attention", Summary: "The local core is available, but its remote listeners could not start.", Detail: err.Error(), Guidance: "Review Application Settings › Network › Remote access and select free reachable listen addresses. Existing work and device credentials were retained.", Target: statuscenter.RepairNetwork}
}

func resumeStartupIssue(id string, err error) statuscenter.OperationIssue {
	return statuscenter.OperationIssue{Title: "Session could not be resumed", Summary: fmt.Sprintf("Crude opened the workspace without resuming %q.", id), Detail: err.Error(), Guidance: "Choose an available session from the launcher or repair its saved account and session data.", Target: statuscenter.RepairSessionAccount}
}
