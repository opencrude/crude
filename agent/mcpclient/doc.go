// Package mcpclient connects Model Context Protocol servers over stdio, HTTP
// and SSE, and projects their tools, resources and prompts for agent calls. It
// owns the connection lifecycle, OAuth and result bounds; which servers an
// agent may use is decided by agent/capability.
package mcpclient
