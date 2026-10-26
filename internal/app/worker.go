package app

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kczpl/yet-another-go-boilerplate/internal/auth"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/config"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/queue"
)

// NewWorker wires the background jobs of every feature and returns the
// worker. This is the wiring point of the jobs, as New is the wiring point
// of the routes: construct the service of a feature, then call its Jobs
// function below. The worker subcommand of cmd/api runs the result.
func NewWorker(logger *slog.Logger, cfg config.Config, pool *pgxpool.Pool) *queue.Worker {
	sessions := auth.NewService(auth.NewRepo(pool), sessionTTL, !cfg.Development())

	w := queue.NewWorker(pool, logger)
	auth.Jobs(w, sessions)
	return w
}
