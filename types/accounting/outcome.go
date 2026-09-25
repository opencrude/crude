// Package accounting contains provider-independent request accounting contracts.
package accounting

type Outcome string

const (
	Unknown     Outcome = ""
	Succeeded   Outcome = "succeeded"
	Failed      Outcome = "failed"
	Interrupted Outcome = "interrupted"
)

func (o Outcome) Known() bool {
	return o == Succeeded || o == Failed || o == Interrupted
}
