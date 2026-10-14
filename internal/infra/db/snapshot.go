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
	Points  []pointScoreJSON   `json:"points"`
	Routes  []routeScoreJSON   `json:"routes"`
	Missing missingAnchorsJSON `json:"missing,omitzero"`
}

type missingAnchorsJSON struct {
	PointIDs []string `json:"points,omitempty"`
	RouteIDs []string `json:"routes,omitempty"`
}

type pointScoreJSON struct {
	PointID        string   `json:"point_id"`
	PeopleScore    *float64 `json:"people_score"`
	OccupancyDelta float64  `json:"occupancy_delta"`
	Exhaustive     bool     `json:"exhaustive"`
}

type routeScoreJSON struct {
	RouteID         string   `json:"route_id"`
	Forward         float64  `json:"forward"`
	Backward        float64  `json:"backward"`
	ScalarFlow      *float64 `json:"scalar_flow,omitempty"`
	StagnationScore *float64 `json:"stagnation_score"`
	Exhaustive      bool     `json:"exhaustive"`
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
		Points:  make([]pointScoreJSON, 0, len(snapshot.PointScores)),
		Routes:  make([]routeScoreJSON, 0, len(snapshot.RouteScores)),
		Missing: missingAnchorsJSON(snapshot.Missing),
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

	return RunInTransaction(ctx, r.db, func(ctx context.Context) error {
		executor := Executor(ctx, r.db)

		_, err := executor.ExecContext(ctx, `
			INSERT INTO snapshots (snapshot_id, tenant_public_id, event_id, window_start, window_end, scores)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			snapshot.ID, tenantPublicID, snapshot.EventID, snapshot.WindowStart, snapshot.WindowEnd, encoded)
		if err != nil {
			return fmt.Errorf("insert snapshot: %w", err)
		}

		for _, score := range snapshot.RouteScores {
			if score.StagnationScore == nil {
				continue
			}

			_, err := executor.ExecContext(ctx, `
				INSERT INTO arc_stagnation_hourly AS h (
					event_id, hour_start, route_id, stagnation_bucket, sample_count, stagnation_sum, first_start, last_end,
					flow_sum, flow_square_sum, flow_stagnation_sum)
				VALUES ($1, date_trunc('hour', $2::timestamptz, 'UTC'), $3, arc_stagnation_bucket($4), 1, $4, $2, $5,
					$6::float8, $6::float8 * $6::float8, $6::float8 * $4)
				ON CONFLICT (event_id, hour_start, route_id, stagnation_bucket) DO UPDATE
				SET sample_count = h.sample_count + 1,
					stagnation_sum = h.stagnation_sum + EXCLUDED.stagnation_sum,
					first_start = LEAST(h.first_start, EXCLUDED.first_start),
					last_end = GREATEST(h.last_end, EXCLUDED.last_end),
					flow_sum = h.flow_sum + EXCLUDED.flow_sum,
					flow_square_sum = h.flow_square_sum + EXCLUDED.flow_square_sum,
					flow_stagnation_sum = h.flow_stagnation_sum + EXCLUDED.flow_stagnation_sum`,
				snapshot.EventID, snapshot.WindowStart, score.RouteID, *score.StagnationScore, snapshot.WindowEnd,
				score.Forward+score.Backward)
			if err != nil {
				return fmt.Errorf("upsert arc stagnation: %w", err)
			}
		}

		return nil
	})
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

type arcHistoryRow struct {
	RouteID           string    `db:"route_id"`
	SampleCount       int       `db:"sample_count"`
	P90Stagnation     float64   `db:"p90_stagnation"`
	MeanStagnation    float64   `db:"mean_stagnation"`
	FirstStart        time.Time `db:"first_start"`
	LastEnd           time.Time `db:"last_end"`
	FlowSum           float64   `db:"flow_sum"`
	FlowSquareSum     float64   `db:"flow_square_sum"`
	FlowStagnationSum float64   `db:"flow_stagnation_sum"`
}

func (r *PostgresSnapshotRepository) SummarizeArcHistory(
	ctx context.Context,
	eventID string,
	since time.Time,
) ([]domain.ArcHistory, error) {
	var rows []arcHistoryRow

	err := sqlx.SelectContext(ctx, Executor(ctx, r.db), &rows, `
		WITH buckets AS (
			SELECT h.route_id,
				h.stagnation_bucket,
				sum(h.sample_count) AS sample_count,
				sum(h.stagnation_sum) AS stagnation_sum,
				min(h.first_start) AS first_start,
				max(h.last_end) AS last_end,
				sum(h.flow_sum) AS flow_sum,
				sum(h.flow_square_sum) AS flow_square_sum,
				sum(h.flow_stagnation_sum) AS flow_stagnation_sum
			FROM arc_stagnation_hourly h
			LEFT JOIN anchor_sensor_sets s ON s.event_id = h.event_id AND s.route_id = h.route_id
			WHERE h.event_id = $1 AND h.hour_start >= $2 AND (s.changed_at IS NULL OR h.first_start >= s.changed_at)
			GROUP BY h.route_id, h.stagnation_bucket
		), ranked AS (
			SELECT *,
				sum(sample_count) OVER (PARTITION BY route_id ORDER BY stagnation_bucket) AS cumulative,
				sum(sample_count) OVER (PARTITION BY route_id) AS total
			FROM buckets
		)
		SELECT route_id,
			max(total) AS sample_count,
			min(stagnation_bucket) FILTER (WHERE cumulative >= 0.9 * total) AS p90_stagnation,
			sum(stagnation_sum) / max(total) AS mean_stagnation,
			min(first_start) AS first_start,
			max(last_end) AS last_end,
			sum(flow_sum) AS flow_sum,
			sum(flow_square_sum) AS flow_square_sum,
			sum(flow_stagnation_sum) AS flow_stagnation_sum
		FROM ranked
		GROUP BY route_id
		ORDER BY route_id`, eventID, since)
	if err != nil {
		return nil, fmt.Errorf("summarize arc history: %w", err)
	}

	histories := make([]domain.ArcHistory, 0, len(rows))
	for _, row := range rows {
		histories = append(histories, domain.ArcHistory(row))
	}

	return histories, nil
}

func (r *PostgresSnapshotRepository) EarliestSnapshotStart(ctx context.Context, eventID string) (time.Time, error) {
	var earliest sql.NullTime

	err := sqlx.GetContext(ctx, Executor(ctx, r.db), &earliest, `
		SELECT min(window_start) FROM snapshots WHERE event_id = $1`, eventID)
	if err != nil {
		return time.Time{}, fmt.Errorf("select earliest snapshot start: %w", err)
	}

	if !earliest.Valid {
		return time.Time{}, repository.ErrSnapshotNotFound
	}

	return earliest.Time, nil
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
		Missing:     domain.MissingAnchors(scores.Missing),
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
	SnapshotID         string    `db:"snapshot_id"`
	ServerTime         time.Time `db:"server_time"`
	Verdict            string    `db:"verdict"`
	DetectionState     []byte    `db:"detection_state"`
	OptimizationResult []byte    `db:"optimization_result"`
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
		SELECT o.snapshot_id, s.window_end AS server_time, o.verdict, o.detection_state, o.optimization_result
		FROM optimization_results o
		JOIN snapshots s ON s.snapshot_id = o.snapshot_id
		WHERE o.event_id = $1
		ORDER BY o.id DESC
		LIMIT 1`, eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.OptimizationOutcome{}, repository.ErrOptimizationOutcomeMissing
	}

	if err != nil {
		return domain.OptimizationOutcome{}, fmt.Errorf("select optimization result: %w", err)
	}

	return domain.OptimizationOutcome(row), nil
}

type sensorSetRow struct {
	PointID             string     `db:"point_id"`
	RouteID             string     `db:"route_id"`
	ObservationPointIDs []byte     `db:"observation_point_ids"`
	ChangedAt           *time.Time `db:"changed_at"`
}

func (r *PostgresSnapshotRepository) ListSensorSets(ctx context.Context, eventID string) ([]domain.SensorSet, error) {
	var rows []sensorSetRow

	err := sqlx.SelectContext(ctx, Executor(ctx, r.db), &rows, `
		SELECT point_id, route_id, observation_point_ids, changed_at
		FROM anchor_sensor_sets
		WHERE event_id = $1
		ORDER BY point_id, route_id`, eventID)
	if err != nil {
		return nil, fmt.Errorf("select sensor sets: %w", err)
	}

	sets := make([]domain.SensorSet, 0, len(rows))

	for _, row := range rows {
		set := domain.SensorSet{
			Anchor:              domain.GraphAnchor{PointID: row.PointID, RouteID: row.RouteID},
			ObservationPointIDs: nil,
			ChangedAt:           time.Time{},
		}

		if err := json.Unmarshal(row.ObservationPointIDs, &set.ObservationPointIDs); err != nil {
			return nil, fmt.Errorf("decode sensor set: %w", err)
		}

		if row.ChangedAt != nil {
			set.ChangedAt = *row.ChangedAt
		}

		sets = append(sets, set)
	}

	return sets, nil
}

func (r *PostgresSnapshotRepository) SaveSensorSets(ctx context.Context, eventID string, sets []domain.SensorSet) error {
	return RunInTransaction(ctx, r.db, func(ctx context.Context) error {
		for _, set := range sets {
			ids, err := json.Marshal(set.ObservationPointIDs)
			if err != nil {
				return fmt.Errorf("encode sensor set: %w", err)
			}

			var changedAt *time.Time
			if !set.ChangedAt.IsZero() {
				changedAt = &set.ChangedAt
			}

			_, err = Executor(ctx, r.db).ExecContext(ctx, `
				INSERT INTO anchor_sensor_sets (event_id, point_id, route_id, observation_point_ids, changed_at)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (event_id, point_id, route_id) DO UPDATE
				SET observation_point_ids = EXCLUDED.observation_point_ids, changed_at = EXCLUDED.changed_at`,
				eventID, set.Anchor.PointID, set.Anchor.RouteID, ids, changedAt)
			if err != nil {
				return fmt.Errorf("upsert sensor set: %w", err)
			}
		}

		return nil
	})
}

func nullableJSON(value []byte) any {
	if value == nil {
		return nil
	}

	return value
}
