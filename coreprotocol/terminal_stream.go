package coreprotocol

import (
	"crude/terminalvt"
	"crude/workspace"
)

// TerminalFrame commits changed packed rows against Base. Metadata contains no
// cells: row data has one bounded, compact representation on the wire.
type TerminalFrame struct {
	Base       uint64
	Projection workspace.TerminalProjection
	Rows       []TerminalRow
	StyleBase  int
	Styles     []terminalvt.Cell
	Scrolls    []TerminalScroll
	Fault      *Fault
}

// TerminalScroll copies the old [Top, Bottom) rows by Rows (positive is up).
// Changed rows following the operation fill the exposed interval.
type TerminalScroll struct{ Top, Bottom, Rows int }

type TerminalRow struct {
	Index int
	Data  []byte
}

// TerminalInputBatch is a contiguous ordered log, retained by the sender until
// acknowledged. Its stream ID is stable for the lifetime of the exact lease.
type TerminalInputBatch struct {
	First  uint64
	Ack    uint64
	Inputs []workspace.TerminalInput
}

type TerminalInputAck struct {
	Seq      uint64
	Failures []TerminalInputFailure
	Fault    *Fault
}

type TerminalInputFailure struct {
	Seq     uint64
	Message string
}

// TerminalViewport changes a grid subscription's history anchor. History pages
// and selection reads remain bounded on-demand commands.
type TerminalViewport struct {
	Request           terminalvt.RowRequest
	DisplayIntervalNS int64
}
