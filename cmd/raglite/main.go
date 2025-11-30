// Command raglite is the CLI for GoRAGlite.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"goraglite/internal/db"
	"goraglite/internal/extract"
	"goraglite/internal/merger"
	"goraglite/internal/orchestrator"
	"goraglite/internal/workflow"
)

const version = "2.0.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// Global flags
	dataDir := flag.String("data", defaultDataDir(), "Data directory")
	flag.Parse()

	cmd := os.Args[1]
	args := os.Args[2:]

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	var err error
	switch cmd {
	case "init":
		err = cmdInit(ctx, *dataDir)
	case "ingest":
		err = cmdIngest(ctx, *dataDir, args)
	case "process":
		err = cmdProcess(ctx, *dataDir)
	case "search":
		err = cmdSearch(ctx, *dataDir, args)
	case "status":
		err = cmdStatus(ctx, *dataDir)
	case "run":
		err = cmdRun(ctx, *dataDir, args)
	case "inspect":
		err = cmdInspect(ctx, args)
	case "gc":
		err = cmdGC(ctx, *dataDir, args)
	case "export":
		err = cmdExport(ctx, *dataDir, args)
	case "workflows":
		err = cmdWorkflows(ctx, *dataDir)
	case "version":
		fmt.Printf("GoRAGlite v%s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`GoRAGlite v` + version + ` - SQLite-powered RAG system

Usage:
  raglite <command> [options] [arguments]

Commands:
  init                Initialize data directory
  ingest <path>       Import files into corpus
  process             Process pending files
  search <query>      Search the corpus
  status              Show system status
  run <workflow>      Run a specific workflow
  inspect <run_id>    Inspect a run
  gc                  Garbage collect old runs
  export <format>     Export corpus data
  workflows           List available workflows
  version             Show version
  help                Show this help

Options:
  -data <dir>         Data directory (default: ~/.raglite)

Examples:
  raglite init
  raglite ingest ./documents/
  raglite ingest ./code.go
  raglite process
  raglite search "how to handle errors"
  raglite status
  raglite workflows
  raglite run pdf_chunking_v1
`)
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".raglite"
	}
	return filepath.Join(home, ".raglite")
}

func cmdInit(ctx context.Context, dataDir string) error {
	fmt.Printf("Initializing GoRAGlite in %s\n", dataDir)

	// Create directories
	dirs := []string{
		dataDir,
		filepath.Join(dataDir, "runs"),
		filepath.Join(dataDir, "queue", "pending"),
		filepath.Join(dataDir, "queue", "processing"),
		filepath.Join(dataDir, "queue", "done"),
		filepath.Join(dataDir, "queue", "failed"),
		filepath.Join(dataDir, "snapshots"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	// Initialize databases
	corpusDB, err := db.OpenCorpus(dataDir)
	if err != nil {
		return fmt.Errorf("init corpus db: %w", err)
	}
	defer corpusDB.Close()

	workflowsDB, err := db.OpenWorkflows(dataDir)
	if err != nil {
		return fmt.Errorf("init workflows db: %w", err)
	}
	defer workflowsDB.Close()

	// Load built-in workflows
	loader := workflow.NewLoader(workflowsDB)
	if err := loader.LoadBuiltins(ctx); err != nil {
		fmt.Printf("Warning: could not load built-in workflows: %v\n", err)
	}

	fmt.Println("Initialization complete!")
	fmt.Printf("  Corpus DB: %s/corpus.db\n", dataDir)
	fmt.Printf("  Workflows DB: %s/workflows.db\n", dataDir)
	return nil
}

func cmdIngest(ctx context.Context, dataDir string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: raglite ingest <path> [path...]")
	}

	corpusDB, err := db.OpenCorpus(dataDir)
	if err != nil {
		return err
	}
	defer corpusDB.Close()

	workflowsDB, err := db.OpenWorkflows(dataDir)
	if err != nil {
		return err
	}
	defer workflowsDB.Close()

	runsDir := filepath.Join(dataDir, "runs")
	engine := workflow.NewEngine(corpusDB, workflowsDB, runsDir)
	orch := orchestrator.New(corpusDB, workflowsDB, engine, orchestrator.DefaultConfig(dataDir))

	// Register extractors
	registry := extract.NewRegistry()
	registry.Register(extract.NewPDFExtractor())
	registry.Register(extract.NewDOCXExtractor())
	registry.Register(extract.NewXLSXExtractor())
	registry.Register(extract.NewCodeExtractor())

	var totalIngested int
	for _, path := range args {
		info, err := os.Stat(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: cannot access %s: %v\n", path, err)
			continue
		}

		if info.IsDir() {
			ids, err := orch.IngestDir(ctx, path, true)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: error ingesting %s: %v\n", path, err)
			}
			totalIngested += len(ids)
			fmt.Printf("Ingested %d files from %s\n", len(ids), path)
		} else {
			id, err := orch.Ingest(ctx, path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: error ingesting %s: %v\n", path, err)
				continue
			}
			totalIngested++
			fmt.Printf("Ingested %s (id: %s...)\n", filepath.Base(path), id[:12])
		}
	}

	fmt.Printf("Total: %d files ingested\n", totalIngested)
	return nil
}

func cmdProcess(ctx context.Context, dataDir string) error {
	corpusDB, err := db.OpenCorpus(dataDir)
	if err != nil {
		return err
	}
	defer corpusDB.Close()

	workflowsDB, err := db.OpenWorkflows(dataDir)
	if err != nil {
		return err
	}
	defer workflowsDB.Close()

	runsDir := filepath.Join(dataDir, "runs")
	engine := workflow.NewEngine(corpusDB, workflowsDB, runsDir)
	orch := orchestrator.New(corpusDB, workflowsDB, engine, orchestrator.DefaultConfig(dataDir))

	fmt.Println("Processing pending files...")

	if err := orch.ProcessPending(ctx); err != nil {
		return err
	}

	// Start merger to integrate results
	mergerCfg := merger.DefaultConfig(dataDir)
	m, err := merger.New(corpusDB, mergerCfg)
	if err != nil {
		return err
	}

	// Process any pending runs
	status, _ := m.Status()
	if status.PendingCount > 0 {
		fmt.Printf("Merging %d pending runs...\n", status.PendingCount)
		// Process all pending
		for i := 0; i < status.PendingCount; i++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}
	}

	fmt.Println("Processing complete!")
	return nil
}

func cmdSearch(ctx context.Context, dataDir string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: raglite search <query>")
	}

	query := strings.Join(args, " ")

	corpusDB, err := db.OpenCorpus(dataDir)
	if err != nil {
		return err
	}
	defer corpusDB.Close()

	workflowsDB, err := db.OpenWorkflows(dataDir)
	if err != nil {
		return err
	}
	defer workflowsDB.Close()

	runsDir := filepath.Join(dataDir, "runs")
	engine := workflow.NewEngine(corpusDB, workflowsDB, runsDir)
	orch := orchestrator.New(corpusDB, workflowsDB, engine, orchestrator.DefaultConfig(dataDir))

	fmt.Printf("Searching for: %s\n\n", query)

	results, err := orch.Search(ctx, query, 10)
	if err != nil {
		return err
	}

	if len(results) == 0 {
		fmt.Println("No results found.")
		return nil
	}

	for i, r := range results {
		fmt.Printf("%d. [%.3f] %s\n", i+1, r.Score, r.ChunkID[:12])
		if r.Snippet != "" {
			// Truncate snippet
			snippet := r.Snippet
			if len(snippet) > 150 {
				snippet = snippet[:150] + "..."
			}
			fmt.Printf("   %s\n", strings.ReplaceAll(snippet, "\n", " "))
		}
		fmt.Println()
	}

	return nil
}

func cmdStatus(ctx context.Context, dataDir string) error {
	corpusDB, err := db.OpenCorpus(dataDir)
	if err != nil {
		return err
	}
	defer corpusDB.Close()

	workflowsDB, err := db.OpenWorkflows(dataDir)
	if err != nil {
		return err
	}
	defer workflowsDB.Close()

	runsDir := filepath.Join(dataDir, "runs")
	engine := workflow.NewEngine(corpusDB, workflowsDB, runsDir)
	orch := orchestrator.New(corpusDB, workflowsDB, engine, orchestrator.DefaultConfig(dataDir))

	status, err := orch.Status(ctx)
	if err != nil {
		return err
	}

	// Get DB stats
	corpusStats, _ := corpusDB.GetStats(ctx)

	fmt.Println("GoRAGlite Status")
	fmt.Println("================")
	fmt.Printf("Data Directory: %s\n\n", dataDir)

	fmt.Println("Corpus:")
	fmt.Printf("  Pending files:   %d\n", status.PendingFiles)
	fmt.Printf("  Processed files: %d\n", status.ProcessedFiles)
	fmt.Printf("  Total chunks:    %d\n", status.TotalChunks)
	fmt.Printf("  Total vectors:   %d\n", status.TotalVectors)
	if corpusStats != nil {
		fmt.Printf("  DB size:         %.2f MB\n", float64(corpusStats.SizeBytes)/(1024*1024))
	}
	fmt.Println()

	fmt.Println("Workflows:")
	for _, w := range status.Workflows {
		fmt.Printf("  - %s\n", w)
	}
	fmt.Println()

	// Merger status
	mergerCfg := merger.DefaultConfig(dataDir)
	m, _ := merger.New(corpusDB, mergerCfg)
	if m != nil {
		mStatus, _ := m.Status()
		if mStatus != nil {
			fmt.Println("Merger Queue:")
			fmt.Printf("  Pending: %d\n", mStatus.PendingCount)
			fmt.Printf("  Done:    %d\n", mStatus.DoneCount)
			fmt.Printf("  Failed:  %d\n", mStatus.FailedCount)
		}
	}

	return nil
}

func cmdRun(ctx context.Context, dataDir string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: raglite run <workflow_id>")
	}

	workflowID := args[0]

	corpusDB, err := db.OpenCorpus(dataDir)
	if err != nil {
		return err
	}
	defer corpusDB.Close()

	workflowsDB, err := db.OpenWorkflows(dataDir)
	if err != nil {
		return err
	}
	defer workflowsDB.Close()

	runsDir := filepath.Join(dataDir, "runs")
	engine := workflow.NewEngine(corpusDB, workflowsDB, runsDir)

	fmt.Printf("Running workflow: %s\n", workflowID)

	cfg := workflow.RunConfig{
		Debug: true,
	}

	run, err := engine.Run(ctx, workflowID, cfg)
	if err != nil {
		return err
	}

	fmt.Printf("Run completed: %s\n", run.ID)
	fmt.Printf("Status: %s\n", run.Status)
	fmt.Printf("Duration: %v\n", run.FinishedAt.Sub(run.StartedAt))

	return nil
}

func cmdInspect(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: raglite inspect <run_db_path>")
	}

	dbPath := args[0]

	runDB, err := db.Open(db.DefaultConfig(dbPath, db.DBTypeRun))
	if err != nil {
		return err
	}
	defer runDB.Close()

	// Get run metadata
	var runID, workflowID, status string
	var startedAt, finishedAt string
	err = runDB.QueryRowContext(ctx, `
		SELECT run_id, workflow_id, status, started_at, COALESCE(finished_at, '')
		FROM _run_meta LIMIT 1
	`).Scan(&runID, &workflowID, &status, &startedAt, &finishedAt)
	if err != nil {
		return fmt.Errorf("read run metadata: %w", err)
	}

	fmt.Println("Run Details")
	fmt.Println("===========")
	fmt.Printf("ID:       %s\n", runID)
	fmt.Printf("Workflow: %s\n", workflowID)
	fmt.Printf("Status:   %s\n", status)
	fmt.Printf("Started:  %s\n", startedAt)
	fmt.Printf("Finished: %s\n", finishedAt)
	fmt.Println()

	// Get step executions
	rows, err := runDB.QueryContext(ctx, `
		SELECT step_order, step_name, rows_in, rows_out, duration_ms, delta_score
		FROM _step_executions
		ORDER BY step_order
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	fmt.Println("Steps:")
	fmt.Println("------")
	fmt.Printf("%-4s %-25s %8s %8s %8s %8s\n", "#", "Name", "In", "Out", "Time(ms)", "Delta")

	for rows.Next() {
		var order int
		var name string
		var rowsIn, rowsOut, durationMs int64
		var deltaScore float64
		rows.Scan(&order, &name, &rowsIn, &rowsOut, &durationMs, &deltaScore)
		fmt.Printf("%-4d %-25s %8d %8d %8d %8.2f\n", order, name, rowsIn, rowsOut, durationMs, deltaScore)
	}

	return nil
}

func cmdGC(ctx context.Context, dataDir string, args []string) error {
	corpusDB, err := db.OpenCorpus(dataDir)
	if err != nil {
		return err
	}
	defer corpusDB.Close()

	mergerCfg := merger.DefaultConfig(dataDir)
	m, err := merger.New(corpusDB, mergerCfg)
	if err != nil {
		return err
	}

	maxAge := 7 * 24 * time.Hour // Default 7 days
	if len(args) > 0 {
		d, err := time.ParseDuration(args[0])
		if err == nil {
			maxAge = d
		}
	}

	fmt.Printf("Garbage collecting runs older than %v...\n", maxAge)

	if err := m.GarbageCollect(ctx, maxAge); err != nil {
		return err
	}

	// Vacuum corpus
	fmt.Println("Vacuuming corpus database...")
	if err := corpusDB.Vacuum(ctx); err != nil {
		return err
	}

	fmt.Println("GC complete!")
	return nil
}

func cmdExport(ctx context.Context, dataDir string, args []string) error {
	format := "json"
	if len(args) > 0 {
		format = args[0]
	}

	corpusDB, err := db.OpenCorpus(dataDir)
	if err != nil {
		return err
	}
	defer corpusDB.Close()

	rows, err := corpusDB.QueryContext(ctx, `
		SELECT c.id, c.file_id, c.content, c.token_count, c.chunk_type, r.source_path
		FROM chunks c
		JOIN raw_files r ON c.file_id = r.id
		ORDER BY r.source_path, c.position
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	switch format {
	case "json":
		var chunks []map[string]interface{}
		for rows.Next() {
			var id, fileID, content, chunkType, sourcePath string
			var tokenCount int
			rows.Scan(&id, &fileID, &content, &tokenCount, &chunkType, &sourcePath)
			chunks = append(chunks, map[string]interface{}{
				"id":          id,
				"file_id":     fileID,
				"content":     content,
				"token_count": tokenCount,
				"chunk_type":  chunkType,
				"source_path": sourcePath,
			})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(chunks)

	case "csv":
		fmt.Println("id,file_id,token_count,chunk_type,source_path")
		for rows.Next() {
			var id, fileID, content, chunkType, sourcePath string
			var tokenCount int
			rows.Scan(&id, &fileID, &content, &tokenCount, &chunkType, &sourcePath)
			fmt.Printf("%s,%s,%d,%s,%s\n", id, fileID, tokenCount, chunkType, sourcePath)
		}
		return nil

	default:
		return fmt.Errorf("unknown format: %s (supported: json, csv)", format)
	}
}

func cmdWorkflows(ctx context.Context, dataDir string) error {
	workflowsDB, err := db.OpenWorkflows(dataDir)
	if err != nil {
		return err
	}
	defer workflowsDB.Close()

	loader := workflow.NewLoader(workflowsDB)
	workflows, err := loader.ListWorkflows(ctx)
	if err != nil {
		return err
	}

	if len(workflows) == 0 {
		fmt.Println("No workflows found. Run 'raglite init' to load built-in workflows.")
		return nil
	}

	fmt.Println("Available Workflows")
	fmt.Println("===================")

	for _, w := range workflows {
		tags, _ := loader.GetWorkflowTags(ctx, w.ID)
		fmt.Printf("\n%s (v%d) [%s]\n", w.ID, w.Version, w.Status)
		fmt.Printf("  %s\n", w.Name)
		if w.Description != "" {
			fmt.Printf("  %s\n", w.Description)
		}
		if len(tags) > 0 {
			fmt.Printf("  Tags: %s\n", strings.Join(tags, ", "))
		}
	}

	return nil
}
