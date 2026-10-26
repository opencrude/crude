package perfdash

import (
	"crude/metrics"
	"crude/perfdata"
)

type Data perfdata.Data
type process = perfdata.Process

const (
	processShell = perfdata.ProcessShell
	processCore  = perfdata.ProcessCore
)

type processedEvent = perfdata.ProcessedEvent

func (d Data) snapshot(p process) *metrics.Snapshot { return perfdata.Data(d).Snapshot(p) }
func (d Data) processes() []process                 { return perfdata.Data(d).Processes() }
func (d Data) latency(p process, name string) (metrics.LatencySeries, bool) {
	return perfdata.Data(d).Latency(p, name)
}
func (d Data) counter(p process, name string) (metrics.CounterSeries, bool) {
	return perfdata.Data(d).Counter(p, name)
}
func (d Data) gauge(p process, name string) (metrics.GaugeSeries, bool) {
	return perfdata.Data(d).Gauge(p, name)
}
func (d Data) table(p process, name string) (metrics.TableSeries, bool) {
	return perfdata.Data(d).Table(p, name)
}
func (d Data) events() []processedEvent      { return perfdata.Data(d).Events() }
func (d Data) busySeries() ([]float64, bool) { return perfdata.Data(d).BusySeries() }
func (d Data) recentBusy() float64           { return perfdata.Data(d).RecentBusy() }
func (d Data) windowSeconds() int            { return perfdata.Data(d).WindowSeconds() }
func shortKey(key string) string             { return perfdata.ShortKey(key) }
