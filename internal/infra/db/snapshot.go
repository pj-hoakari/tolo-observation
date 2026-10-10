package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
)

type PostgresSnapshotRepository struct {
	db *sqlx.DB
}

func NewPostgresSnapshotRepository(db *sqlx.DB) *PostgresSnapshotRepository {
	return &PostgresSnapshotRepository{db: db}
}

type snapshotScores struct {
	Points []pointScoreJSON `json:"points"`
	Routes []routeScoreJSON `json:"routes"`
}

type pointScoreJSON struct {
	PointID        string  `json:"point_id"`
	PeopleScore    float64 `json:"people_score"`
	OccupancyDelta float64 `json:"occupancy_delta"`
	Exhaustive     bool    `json:"exhaustive"`
}

type routeScoreJSON struct {
	RouteID         string  `json:"route_id"`
	Forward         float64 `json:"forward"`
	Backward        float64 `json:"backward"`
	StagnationScore float64 `json:"stagnation_score"`
	Exhaustive      bool    `json:"exhaustive"`
}

type snapshotRow struct {
	ID          string    `db:"snapshot_id"`
	EventID     string    `db:"event_id"`
	WindowStart time.Time `db:"window_start"`
	WindowEnd   time.Time `db:"window_end"`
	Scores      []byte    `db:"scores"`
}

func (r *PostgresSnapshotRepository) SaveSnapshot(
	ctx context.Context,
	tenantPublicID string,
	snapshot domain.Snapshot,
) error {
	scores := snapshotScores{
		Points: make([]pointScoreJSON, 0, len(snapshot.PointScores)),
		Routes: make([]routeScoreJSON, 0, len(snapshot.RouteScores)),
	}

	for _, score := range snapshot.PointScores {
		scores.Points = append(scores.Points, pointScoreJSON(score))
	}

	for _, score := range snapshot.RouteScores {
		scores.Routes = append(scores.Routes, routeScoreJSON(score))
	}

	encoded, err := json.Marshal(scores)
	if err != nil {
		return fmt.Errorf("encode snapshot scores: %w", err)
	}

	_, err = Executor(ctx, r.db).ExecContext(ctx, `
		INSERT INTO snapshots (snapshot_id, tenant_public_id, event_id, window_start, window_end, scores)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		snapshot.ID, tenantPublicID, snapshot.EventID, snapshot.WindowStart, snapshot.WindowEnd, encoded)
	if err != nil {
		return fmt.Errorf("insert snapshot: %w", err)
	}

	return nil
}

func (r *PostgresSnapshotRepository) LatestSnapshot(ctx context.Context, eventID string) (domain.Snapshot, error) {
	var row snapshotRow

	err := sqlx.GetContext(ctx, Executor(ctx, r.db), &row, `
		SELECT snapshot_id, event_id, window_start, window_end, scores
		FROM snapshots
		WHERE event_id = $1
		ORDER BY id DESC
		LIMIT 1`, eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Snapshot{}, repository.ErrSnapshotNotFound
	}

	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("select snapshot: %w", err)
	}

	return row.snapshot()
}

func (r *PostgresSnapshotRepository) ListSnapshotsEndingAfter(
	ctx context.Context,
	eventID string,
	after time.Time,
) ([]domain.Snapshot, error) {
	var rows []snapshotRow

	err := sqlx.SelectContext(ctx, Executor(ctx, r.db), &rows, `
		SELECT snapshot_id, event_id, window_start, window_end, scores
		FROM snapshots
		WHERE event_id = $1 AND window_end > $2
		ORDER BY window_end, id`, eventID, after)
	if err != nil {
		return nil, fmt.Errorf("select snapshots: %w", err)
	}

	snapshots := make([]domain.Snapshot, 0, len(rows))

	for _, row := range rows {
		snapshot, err := row.snapshot()
		if err != nil {
			return nil, err
		}

		snapshots = append(snapshots, snapshot)
	}

	return snapshots, nil
}

func (row snapshotRow) snapshot() (domain.Snapshot, error) {
	var scores snapshotScores
	if err := json.Unmarshal(row.Scores, &scores); err != nil {
		return domain.Snapshot{}, fmt.Errorf("decode snapshot scores: %w", err)
	}

	snapshot := domain.Snapshot{
		ID:          row.ID,
		EventID:     row.EventID,
		WindowStart: row.WindowStart,
		WindowEnd:   row.WindowEnd,
		PointScores: make([]domain.PointScore, 0, len(scores.Points)),
		RouteScores: make([]domain.RouteScore, 0, len(scores.Routes)),
	}

	for _, score := range scores.Points {
		snapshot.PointScores = append(snapshot.PointScores, domain.PointScore(score))
	}

	for _, score := range scores.Routes {
		snapshot.RouteScores = append(snapshot.RouteScores, domain.RouteScore(score))
	}

	return snapshot, nil
}

type optimizationOutcomeRow struct {
	SnapshotID         string `db:"snapshot_id"`
	Verdict            string `db:"verdict"`
	DetectionState     []byte `db:"detection_state"`
	OptimizationResult []byte `db:"optimization_result"`
}

func (r *PostgresSnapshotRepository) SaveOptimizationOutcome(
	ctx context.Context,
	eventID string,
	outcome domain.OptimizationOutcome,
) error {
	_, err := Executor(ctx, r.db).ExecContext(ctx, `
		INSERT INTO optimization_results (event_id, snapshot_id, verdict, detection_state, optimization_result)
		VALUES ($1, $2, $3, $4, $5)`,
		eventID, outcome.SnapshotID, outcome.Verdict, outcome.DetectionState, nullableJSON(outcome.OptimizationResult))
	if err != nil {
		return fmt.Errorf("insert optimization result: %w", err)
	}

	return nil
}

func (r *PostgresSnapshotRepository) LatestOptimizationOutcome(
	ctx context.Context,
	eventID string,
) (domain.OptimizationOutcome, error) {
	var row optimizationOutcomeRow

	err := sqlx.GetContext(ctx, Executor(ctx, r.db), &row, `
		SELECT snapshot_id, verdict, detection_state, optimization_result
		FROM optimization_results
		WHERE event_id = $1
		ORDER BY id DESC
		LIMIT 1`, eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.OptimizationOutcome{}, repository.ErrOptimizationOutcomeMissing
	}

	if err != nil {
		return domain.OptimizationOutcome{}, fmt.Errorf("select optimization result: %w", err)
	}

	return domain.OptimizationOutcome(row), nil
}

func nullableJSON(value []byte) any {
	if value == nil {
		return nil
	}

	return value
}
