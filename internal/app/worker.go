package app

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kczpl/yet-another-go-boilerplate/internal/auth"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/config"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/jobs"
)

// workerConcurrency is the number of jobs that one worker process runs at
// the same time. A job holds a pool connection only while it runs a
// statement, so the pool size does not limit this value.
const workerConcurrency = 4

// NewWorker wires the background jobs of every feature and returns the
// worker. This is the wiring point of the jobs, as New is the wiring point
// of the routes: construct the service of a feature, then call its Jobs
// function below. The worker subcommand of cmd/api runs the result.
func NewWorker(logger *slog.Logger, cfg config.Config, pool *pgxpool.Pool) *jobs.Worker {
	sessions := auth.NewService(auth.NewRepo(pool), sessionTTL, !cfg.Development())

	w := jobs.NewWorker(pool, logger, workerConcurrency)
	auth.Jobs(w, sessions)
	return w
}
