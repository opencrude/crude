package app

import (
	"context"
	"os"
	"time"

	"crude/internal/perfdata"
)

// WritePerformanceReport saves a performance report from this computer's
// saved history without starting a shell or a core, so a shell that does not
// respond can still be reported. It returns the report's path.
func WritePerformanceReport(ctx context.Context, home string) (string, error) {
	report := perfdata.Report{Created: time.Now(), Host: perfdata.CurrentHost(os.Getenv)}
	perfdata.ReadReportHistory(ctx, perfdata.ArchivePath(home), &report)
	return perfdata.SaveReport(perfdata.ReportsDir(home), report)
}
