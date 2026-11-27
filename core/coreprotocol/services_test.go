package coreprotocol

import "testing"

// Requests that wait for a model or a remote server are timed apart from
// local work; sending a prompt and reading Git state stay local requests.
func TestRemoteBoundSeparatesModelAndRemoteWork(t *testing.T) {
	for _, method := range []string{AgentCompact, GitCommitMessage, GitFetch, GitPull, GitPush, GitLFS} {
		if !RemoteBound(method) {
			t.Errorf("%s waits for a model or a remote server", method)
		}
	}
	for _, method := range []string{SubmitInput, SteerInput, GitSnapshot, GitCommitModel, GitCommitStaged, Attach} {
		if RemoteBound(method) {
			t.Errorf("%s is local work", method)
		}
	}
}
