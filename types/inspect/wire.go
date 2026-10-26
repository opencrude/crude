package inspect

import "encoding/json"

// Inspection evidence is already a bounded JSON document. The typed carrier
// retains its exact bytes, not a reflected second representation.
func rawJSONToBytes(v json.RawMessage) []byte { return v }
func bytesToRawJSON(b []byte) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	return json.RawMessage(b)
}
