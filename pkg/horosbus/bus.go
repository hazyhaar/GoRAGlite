// Package horosbus provides communication with the HOROS central bus.
// All inter-process communication in HOROS happens via SQLite tables.
//
// Bus location: /data/horos/system/horos_events.db
// Tables: tasks, heartbeats, logs
package horosbus

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DefaultEventsDBPath is the standard location for horos_events.db
const DefaultEventsDBPath = "/data/horos/system/horos_events.db"

// Bus provides access to the HOROS event bus.
type Bus struct {
	db   *sql.DB
	path string
}

// Task represents a pending task from the bus.
type Task struct {
	ID        string
	Zone      string
	Action    string
	Payload   string
	Status    string
	CreatedAt int64
	StartedAt int64
	WorkerID  string
}

// HeartbeatMetrics contains worker health metrics.
type HeartbeatMetrics struct {
	TasksProcessed int64   `json:"tasks_processed"`
	TasksFailed    int64   `json:"tasks_failed"`
	MemoryMB       float64 `json:"memory_mb,omitempty"`
	UptimeSeconds  float64 `json:"uptime_seconds"`
}

// Connect connects to the HOROS event bus.
func Connect(path string) (*Bus, error) {
	if path == "" {
		path = DefaultEventsDBPath
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open events db: %w", err)
	}

	// Set HOROS-required pragmas
	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("set pragma: %w", err)
		}
	}

	// Ensure tables exist
	if err := initBusSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("init bus schema: %w", err)
	}

	return &Bus{db: db, path: path}, nil
}

// initBusSchema creates the bus tables if they don't exist.
func initBusSchema(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS tasks (
		task_id TEXT PRIMARY KEY,
		zone TEXT NOT NULL,
		action TEXT NOT NULL,
		payload TEXT,
		status TEXT NOT NULL DEFAULT 'pending',
		created_at INTEGER NOT NULL,
		started_at INTEGER,
		finished_at INTEGER,
		worker_id TEXT,
		result TEXT,
		error TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_tasks_zone_status ON tasks(zone, status);
	CREATE INDEX IF NOT EXISTS idx_tasks_created ON tasks(created_at);

	CREATE TABLE IF NOT EXISTS heartbeats (
		worker_id TEXT PRIMARY KEY,
		zone TEXT NOT NULL,
		last_beat INTEGER NOT NULL,
		metrics TEXT,
		status TEXT NOT NULL DEFAULT 'alive'
	);

	CREATE INDEX IF NOT EXISTS idx_heartbeats_zone ON heartbeats(zone);

	CREATE TABLE IF NOT EXISTS logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp INTEGER NOT NULL,
		worker_id TEXT,
		zone TEXT,
		level TEXT NOT NULL,
		message TEXT NOT NULL,
		context TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_logs_timestamp ON logs(timestamp);
	CREATE INDEX IF NOT EXISTS idx_logs_zone ON logs(zone);
	`
	_, err := db.Exec(schema)
	return err
}

// Close closes the bus connection.
func (b *Bus) Close() error {
	return b.db.Close()
}

// SubmitTask submits a new task to a zone.
func (b *Bus) SubmitTask(ctx context.Context, zone, action string, payload interface{}) (string, error) {
	taskID := uuid.New().String()
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	_, err = b.db.ExecContext(ctx, `
		INSERT INTO tasks (task_id, zone, action, payload, status, created_at)
		VALUES (?, ?, ?, ?, 'pending', ?)
	`, taskID, zone, action, string(payloadJSON), time.Now().Unix())
	if err != nil {
		return "", fmt.Errorf("insert task: %w", err)
	}

	return taskID, nil
}

// ClaimTask claims a pending task for a zone.
// Returns nil if no tasks are available.
func (b *Bus) ClaimTask(ctx context.Context, zone, workerID string) (*Task, error) {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var task Task
	err = tx.QueryRowContext(ctx, `
		SELECT task_id, zone, action, payload, created_at
		FROM tasks
		WHERE zone = ? AND status = 'pending'
		ORDER BY created_at ASC
		LIMIT 1
	`, zone).Scan(&task.ID, &task.Zone, &task.Action, &task.Payload, &task.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, nil // No tasks available
	}
	if err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	_, err = tx.ExecContext(ctx, `
		UPDATE tasks
		SET status = 'running', worker_id = ?, started_at = ?
		WHERE task_id = ?
	`, workerID, now, task.ID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	task.Status = "running"
	task.WorkerID = workerID
	task.StartedAt = now
	return &task, nil
}

// CompleteTask marks a task as completed with a result.
func (b *Bus) CompleteTask(ctx context.Context, taskID string, result interface{}) error {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}

	_, err = b.db.ExecContext(ctx, `
		UPDATE tasks
		SET status = 'completed', finished_at = ?, result = ?
		WHERE task_id = ?
	`, time.Now().Unix(), string(resultJSON), taskID)
	return err
}

// FailTask marks a task as failed with an error message.
func (b *Bus) FailTask(ctx context.Context, taskID string, errMsg string) error {
	_, err := b.db.ExecContext(ctx, `
		UPDATE tasks
		SET status = 'failed', finished_at = ?, error = ?
		WHERE task_id = ?
	`, time.Now().Unix(), errMsg, taskID)
	return err
}

// GetTaskStatus returns the status of a task.
func (b *Bus) GetTaskStatus(ctx context.Context, taskID string) (string, error) {
	var status string
	err := b.db.QueryRowContext(ctx,
		"SELECT status FROM tasks WHERE task_id = ?",
		taskID,
	).Scan(&status)
	return status, err
}

// SendHeartbeat sends a heartbeat for a worker.
// HOROS requires heartbeats every 15 seconds.
func (b *Bus) SendHeartbeat(ctx context.Context, workerID, zone string, metrics *HeartbeatMetrics) error {
	metricsJSON, _ := json.Marshal(metrics)

	_, err := b.db.ExecContext(ctx, `
		INSERT INTO heartbeats (worker_id, zone, last_beat, metrics, status)
		VALUES (?, ?, ?, ?, 'alive')
		ON CONFLICT(worker_id) DO UPDATE SET
			last_beat = excluded.last_beat,
			metrics = excluded.metrics,
			status = 'alive'
	`, workerID, zone, time.Now().Unix(), string(metricsJSON))
	return err
}

// MarkDead marks a worker as dead (called by meta-orchestrator).
func (b *Bus) MarkDead(ctx context.Context, workerID string) error {
	_, err := b.db.ExecContext(ctx, `
		UPDATE heartbeats SET status = 'dead' WHERE worker_id = ?
	`, workerID)
	return err
}

// Log writes a log entry to the bus.
func (b *Bus) Log(ctx context.Context, workerID, zone, level, message string, logContext interface{}) error {
	var contextJSON string
	if logContext != nil {
		data, _ := json.Marshal(logContext)
		contextJSON = string(data)
	}

	_, err := b.db.ExecContext(ctx, `
		INSERT INTO logs (timestamp, worker_id, zone, level, message, context)
		VALUES (?, ?, ?, ?, ?, ?)
	`, time.Now().Unix(), workerID, zone, level, message, contextJSON)
	return err
}

// LogInfo is a convenience method for info-level logs.
func (b *Bus) LogInfo(ctx context.Context, workerID, zone, message string) error {
	return b.Log(ctx, workerID, zone, "INFO", message, nil)
}

// LogError is a convenience method for error-level logs.
func (b *Bus) LogError(ctx context.Context, workerID, zone, message string, err error) error {
	return b.Log(ctx, workerID, zone, "ERROR", message, map[string]string{"error": err.Error()})
}

// PendingTaskCount returns the number of pending tasks for a zone.
func (b *Bus) PendingTaskCount(ctx context.Context, zone string) (int, error) {
	var count int
	err := b.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM tasks WHERE zone = ? AND status = 'pending'",
		zone,
	).Scan(&count)
	return count, err
}

// Deregister removes a worker from the heartbeats table.
func (b *Bus) Deregister(ctx context.Context, workerID string) error {
	_, err := b.db.ExecContext(ctx, "DELETE FROM heartbeats WHERE worker_id = ?", workerID)
	return err
}
