package types

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// UnmarshalJSON preserves sparse ordinary settings while refusing ambiguous
// outer prediction authority. Otherwise encoding/json's last-member and
// case-insensitive matching could repair corrupted consent during a neighbor
// save. Inner prediction bytes stay untouched and are decoded fail-closed by
// DecodeWorkspacePredictionSettings, so explicit Reset can still remove them.
func (o *WorkspaceOverrides) UnmarshalJSON(raw []byte) error {
	type plain WorkspaceOverrides
	var decoded plain
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		*o = WorkspaceOverrides{}
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return err
	}
	seen := make(map[string]bool, 4)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("invalid workspace preference member")
		}
		for _, name := range []string{"prediction", "prediction_root_identity", "prediction_connection_identity", "prediction_grant_id"} {
			if strings.EqualFold(key, name) {
				if key != name || seen[name] {
					return errors.New("ambiguous stored workspace prediction authority; repair the workspace preference row explicitly")
				}
				seen[name] = true
				break
			}
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	*o = WorkspaceOverrides(decoded)
	return nil
}
