package workflow

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"goraglite/internal/db"
)

//go:embed ../../sql/workflows/*.sql
var workflowsFS embed.FS

// Loader loads workflow definitions from SQL files.
type Loader struct {
	db *db.DB
}

// NewLoader creates a new workflow loader.
func NewLoader(workflowsDB *db.DB) *Loader {
	return &Loader{db: workflowsDB}
}

// LoadBuiltins loads all built-in workflow definitions.
func (l *Loader) LoadBuiltins(ctx context.Context) error {
	entries, err := workflowsFS.ReadDir("../../sql/workflows")
	if err != nil {
		return fmt.Errorf("read workflows dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		path := filepath.Join("../../sql/workflows", entry.Name())
		content, err := workflowsFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read workflow file %s: %w", entry.Name(), err)
		}

		if _, err := l.db.ExecContext(ctx, string(content)); err != nil {
			return fmt.Errorf("execute workflow %s: %w", entry.Name(), err)
		}
	}

	return nil
}

// LoadFromFile loads a workflow definition from a file.
func (l *Loader) LoadFromFile(ctx context.Context, path string) error {
	content, err := fs.ReadFile(workflowsFS, path)
	if err != nil {
		return fmt.Errorf("read workflow file: %w", err)
	}

	if _, err := l.db.ExecContext(ctx, string(content)); err != nil {
		return fmt.Errorf("execute workflow: %w", err)
	}

	return nil
}

// ListWorkflows returns all available workflows.
func (l *Loader) ListWorkflows(ctx context.Context) ([]Workflow, error) {
	rows, err := l.db.QueryContext(ctx, `
		SELECT id, name, version, description, status, created_at
		FROM workflows
		ORDER BY name, version DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workflows []Workflow
	for rows.Next() {
		var w Workflow
		err := rows.Scan(&w.ID, &w.Name, &w.Version, &w.Description, &w.Status, &w.CreatedAt)
		if err != nil {
			return nil, err
		}
		workflows = append(workflows, w)
	}

	return workflows, nil
}

// GetWorkflowTags returns tags for a workflow.
func (l *Loader) GetWorkflowTags(ctx context.Context, workflowID string) ([]string, error) {
	rows, err := l.db.QueryContext(ctx, `
		SELECT tag FROM workflow_tags WHERE workflow_id = ?
	`, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}

	return tags, nil
}

// FindWorkflowsByTag finds workflows with a specific tag.
func (l *Loader) FindWorkflowsByTag(ctx context.Context, tag string) ([]Workflow, error) {
	rows, err := l.db.QueryContext(ctx, `
		SELECT w.id, w.name, w.version, w.description, w.status, w.created_at
		FROM workflows w
		JOIN workflow_tags t ON w.id = t.workflow_id
		WHERE t.tag = ? AND w.status = 'active'
		ORDER BY w.name
	`, tag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workflows []Workflow
	for rows.Next() {
		var w Workflow
		err := rows.Scan(&w.ID, &w.Name, &w.Version, &w.Description, &w.Status, &w.CreatedAt)
		if err != nil {
			return nil, err
		}
		workflows = append(workflows, w)
	}

	return workflows, nil
}

// ActivateWorkflow sets a workflow to active status.
func (l *Loader) ActivateWorkflow(ctx context.Context, workflowID string) error {
	_, err := l.db.ExecContext(ctx, `
		UPDATE workflows SET status = 'active', updated_at = datetime('now')
		WHERE id = ?
	`, workflowID)
	return err
}

// DeprecateWorkflow marks a workflow as deprecated.
func (l *Loader) DeprecateWorkflow(ctx context.Context, workflowID string) error {
	_, err := l.db.ExecContext(ctx, `
		UPDATE workflows SET status = 'deprecated', updated_at = datetime('now')
		WHERE id = ?
	`, workflowID)
	return err
}

// DeleteWorkflow removes a workflow and its steps.
func (l *Loader) DeleteWorkflow(ctx context.Context, workflowID string) error {
	return l.db.Transaction(ctx, func(tx *db.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM workflow_tags WHERE workflow_id = ?", workflowID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM workflow_steps WHERE workflow_id = ?", workflowID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM workflows WHERE id = ?", workflowID); err != nil {
			return err
		}
		return nil
	})
}

// CloneWorkflow creates a copy of a workflow with a new ID.
func (l *Loader) CloneWorkflow(ctx context.Context, sourceID, newID, newName string) error {
	return l.db.Transaction(ctx, func(tx *db.Tx) error {
		// Clone workflow
		_, err := tx.ExecContext(ctx, `
			INSERT INTO workflows (id, name, version, description, input_schema, output_schema, status, created_at, updated_at)
			SELECT ?, ?, 1, description, input_schema, output_schema, 'draft', datetime('now'), datetime('now')
			FROM workflows
			WHERE id = ?
		`, newID, newName, sourceID)
		if err != nil {
			return err
		}

		// Clone steps
		_, err = tx.ExecContext(ctx, `
			INSERT INTO workflow_steps (workflow_id, step_order, step_name, operation, source, predicate, output, config, expects_delta, on_empty)
			SELECT ?, step_order, step_name, operation, source, predicate, output, config, expects_delta, on_empty
			FROM workflow_steps
			WHERE workflow_id = ?
		`, newID, sourceID)
		if err != nil {
			return err
		}

		// Clone tags
		_, err = tx.ExecContext(ctx, `
			INSERT INTO workflow_tags (workflow_id, tag)
			SELECT ?, tag
			FROM workflow_tags
			WHERE workflow_id = ?
		`, newID, sourceID)
		if err != nil {
			return err
		}

		return nil
	})
}

// Tx wraps a database transaction for workflow operations.
type Tx = db.Tx
