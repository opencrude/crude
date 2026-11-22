package coreprotocol

import (
	captypes "crude/types/capability"
	"crude/types/control"
)

const (
	CapabilityHostRegister    = "capability.host.register"
	CapabilityHostReply       = "capability.host.reply"
	CapabilityHostRequest     = "capability.host.request"
	CapabilityHostMaxBytes    = 4 << 20
	CapabilityHostOutstanding = 16
)

func ValidateCapabilityHostRequest(r captypes.HostRequest, workspace string, owner control.ShellOwner) error {
	return captypes.ValidateCapabilityHostRequest(r, workspace, owner)
}
