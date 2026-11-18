package workspace

import (
	"crypto/rand"
	"errors"

	"crude/agent/session"
	"crude/types"
	"crude/workspace/fileio"
)

// The preparation owner captures this binding before starting its worker.
// It cannot follow another workspace, session, account manager or role later.
type predictionPreferenceBinding struct {
	root          string
	completer     session.EditorPredictionCompleter
	connectionErr error
}

func predictionEdits(edits []types.SettingEdit) []types.SettingEdit {
	var result []types.SettingEdit
	for _, edit := range edits {
		if len(edit.Path) > 0 && edit.Path[0] == "prediction" {
			result = append(result, edit)
		}
	}
	return result
}

// Called within the single workspace-preference transaction after patching the
// latest committed overlay. There is deliberately no application/session grant,
// caller-supplied identity, provider request, endpoint probe or inferred budget.
func bindPredictionPreferences(overrides *types.WorkspaceOverrides, edits []types.SettingEdit, binding predictionPreferenceBinding, analysisRoot string) error {
	if len(predictionEdits(edits)) == 0 {
		return nil
	}
	choice := types.DefaultWorkspacePredictionSettings()
	if len(overrides.Prediction) != 0 {
		var err error
		choice, err = types.DecodeWorkspacePredictionSettings(overrides.Prediction)
		if err != nil {
			return err
		}
	}
	overrides.PredictionRootIdentity = ""
	overrides.PredictionConnectionIdentity = ""
	overrides.PredictionGrantID = ""
	if choice.Mode == types.PredictionOff {
		return nil
	}
	if binding.connectionErr != nil {
		// Completer errors are written for the user and hold no secret.
		return errors.New(sentence(binding.connectionErr.Error()))
	}
	if binding.completer.ConnectionIdentity == "" || binding.completer.Current() != nil {
		return errors.New("the suggestion connection changed; try again")
	}
	root, err := fileio.DirectoryIdentity(binding.root)
	if err != nil {
		return errors.New("suggestions need a workspace folder that Crude can identify")
	}
	if analysisRoot != "" && root != analysisRoot {
		return errors.New("workspace directory changed during preference save")
	}
	overrides.PredictionRootIdentity = root
	overrides.PredictionConnectionIdentity = binding.completer.ConnectionIdentity
	overrides.PredictionGrantID = "prediction-" + rand.Text()
	return nil
}
