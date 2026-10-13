package app

import (
	"context"
	"github.com/fdkevin0/MetaDonalds/internal/backend"
	"io"
	"log/slog"
)

func startDownloaders(ctx context.Context, cfg backend.Config, logger *slog.Logger, output io.Writer) []*rcloneProcess {
	processes := []*rcloneProcess{}
	for _, socket := range cfg.RCSockets() {
		p, err := startRclone(ctx, socket, logger, output)
		if err != nil {
			logger.Warn("downloader unavailable; search remains available", "socket", socket, "error", err)
			continue
		}
		if p != nil {
			processes = append(processes, p)
			go func() {
				<-p.done
				logger.Info("rclone process stopped; downloader submissions may be unavailable", "socket", socket)
			}()
		}
	}
	return processes
}
func stopDownloaders(processes []*rcloneProcess) {
	for _, p := range processes {
		p.stop()
	}
}
