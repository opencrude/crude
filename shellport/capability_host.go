package shellport

// CapabilityHost is a passive, connection-owned worker lifecycle. It never owns
// model execution or frontend state, and Start performs no subprocess launch.
type CapabilityHost interface {
	StartCapabilityHost()
	StopCapabilityHost()
}
