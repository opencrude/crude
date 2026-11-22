// Package provider speaks each model wire protocol: it builds requests,
// streams responses, classifies failures and accounts for connections. Session
// policy and prompt content stay outside; callers supply the protocol.
package provider
