package types

import "encoding/json"

// JSON documents (schemas, settings sections, control values) cross the core
// protocol as their own bytes. An empty value is absent: no JSON document is
// empty, and absence is what "inherit" and "not provided" mean here.
func rawJSONToBytes(v json.RawMessage) []byte { return v }
func bytesToRawJSON(b []byte) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	return json.RawMessage(b)
}
