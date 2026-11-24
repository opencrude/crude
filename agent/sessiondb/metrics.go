package sessiondb

import "crude/internal/metrics"

// sessionWrites times durable session writes by operation. Each commit
// syncs the database, so slow disks (and Windows FlushFileBuffers) show here.
var sessionWrites = metrics.NewTable(metrics.Meta{Name: "storage.session", Group: metrics.GroupStorage, Title: "Session database writes",
	Help: "Durable session commits by operation. Agents wait for these before continuing.", Warn: 20_000, Crit: 200_000})

var publicationStages = metrics.NewTable(metrics.Meta{Name: "storage.context.stages", SaveLabels: true, Stall: 50_000, Group: metrics.GroupStorage, Title: "Context publication stages",
	Help: "Compare time to start the transaction, prepare and write its changes, and commit them."})
