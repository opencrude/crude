package session

import (
	"sync"

	"crude/prompt"
	captypes "crude/types/capability"
)

// capabilitySharing tells the agents of one session (a root and its
// children) when one of them makes a capability usable. Without it a parent
// and its child each searched for and tried to install their own screenshot
// MCP in the same turn. Notices reach a recipient at its next request.
type capabilitySharing struct {
	mu     sync.Mutex
	agents map[string]sharingAgent
	inbox  map[string][]string
	shared map[string]bool // operation+id already announced
}

type sharingAgent struct {
	name, parentID string
}

// maxSharedNotices bounds an idle agent's undelivered notices; the newest win.
const maxSharedNotices = 8

func (sharing *capabilitySharing) join(actor captypes.RuntimeActor, name string) {
	sharing.mu.Lock()
	defer sharing.mu.Unlock()
	if sharing.agents == nil {
		sharing.agents = make(map[string]sharingAgent)
		sharing.inbox = make(map[string][]string)
		sharing.shared = make(map[string]bool)
	}
	sharing.agents[actor.ID] = sharingAgent{name: name, parentID: actor.ParentID}
}

// share queues a notice for every other agent of the session, once per
// capability and operation: a child reactivating what its parent announced
// tells nobody anything new.
func (sharing *capabilitySharing) share(actorID, operation, id string) {
	if id == "" {
		return
	}
	sharing.mu.Lock()
	defer sharing.mu.Unlock()
	sender, ok := sharing.agents[actorID]
	if !ok || sharing.shared[operation+"\x00"+id] {
		return
	}
	sharing.shared[operation+"\x00"+id] = true
	for recipientID, recipient := range sharing.agents {
		if recipientID == actorID {
			continue
		}
		relation := "peer"
		switch {
		case recipient.parentID == actorID:
			relation = "parent"
		case sender.parentID == recipientID:
			relation = "child"
		}
		queue := append(sharing.inbox[recipientID], prompt.RuntimeCapabilityShared(sender.name, relation, operation, id))
		if len(queue) > maxSharedNotices {
			queue = queue[len(queue)-maxSharedNotices:]
		}
		sharing.inbox[recipientID] = queue
	}
}

// drain returns and clears an agent's pending notices.
func (sharing *capabilitySharing) drain(agentID string) []string {
	sharing.mu.Lock()
	defer sharing.mu.Unlock()
	notices := sharing.inbox[agentID]
	delete(sharing.inbox, agentID)
	return notices
}

// sharedCapabilityID is the capability a successful operation made usable.
func sharedCapabilityID(tool string, params map[string]any) (string, string) {
	switch tool {
	case "capability_activate":
		id, _ := params["id"].(string)
		return "activated", id
	case "mcp_request_access":
		id, _ := params["connection_id"].(string)
		return "granted access to", id
	}
	return "", ""
}
