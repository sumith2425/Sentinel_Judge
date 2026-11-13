package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sumith2425/sentinel-judge/internal/models"
	"github.com/sumith2425/sentinel-judge/internal/store"
)

func main() {
	bundleDir := flag.String("bundle", "", "Path to directory containing problem folders")
	problemsOut := flag.String("dest", "./testdata/problems", "Destination directory for test vector files")
	pgDSN := flag.String("dsn", "postgres://sentinel:sentinel_pass@localhost:5432/sentinel_judge?sslmode=disable", "PostgreSQL DSN")
	flag.Parse()

	if *bundleDir == "" {
		log.Fatal("Usage: go run cmd/importer/main.go -bundle <path_to_problems_dir>")
	}

	pgStore, err := store.NewPostgresStore(*pgDSN)
	if err != nil {
		log.Fatalf("Failed to connect to PostgreSQL: %v", err)
	}
	defer pgStore.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := pgStore.EnsureProblemSchema(ctx); err != nil {
		log.Fatalf("Schema initialization error: %v", err)
	}

	entries, err := os.ReadDir(*bundleDir)
	if err != nil {
		log.Fatalf("Failed to read bundle directory: %v", err)
	}

	type bundleItem struct {
		prob models.Problem
		dir  string
	}
	var items []bundleItem

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		probDir := filepath.Join(*bundleDir, entry.Name())
		metaPath := filepath.Join(probDir, "problem.json")
		metaBytes, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var p models.Problem
		if err := json.Unmarshal(metaBytes, &p); err != nil {
			log.Printf("Skipping %s: invalid JSON: %v", entry.Name(), err)
			continue
		}
		if p.ID == "" {
			p.ID = entry.Name()
		}
		items = append(items, bundleItem{prob: p, dir: probDir})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].prob.DisplayOrder < items[j].prob.DisplayOrder
	})

	importedCount := 0
	for _, item := range items {
		p := item.prob
		probDir := item.dir

		testsSrc := filepath.Join(probDir, "tests")
		if _, err := os.Stat(testsSrc); os.IsNotExist(err) {
			testsSrc = probDir
		}

		testFiles, err := os.ReadDir(testsSrc)
		if err != nil {
			log.Printf("Skipping %s: cannot read test files: %v", p.ID, err)
			continue
		}

		destDir := filepath.Join(*problemsOut, p.ID)
		if err := os.MkdirAll(destDir, 0755); err != nil {
			log.Fatalf("Failed to create test destination %s: %v", destDir, err)
		}

		inCount := 0
		for _, tf := range testFiles {
			if tf.IsDir() {
				continue
			}
			name := tf.Name()
			if strings.HasSuffix(name, ".in") || strings.HasSuffix(name, ".out") {
				srcFile := filepath.Join(testsSrc, name)
				dstFile := filepath.Join(destDir, name)
				if err := copyFile(srcFile, dstFile); err != nil {
					log.Fatalf("Failed copying %s: %v", srcFile, err)
				}
				if strings.HasSuffix(name, ".in") {
					inCount++
				}
			}
		}

		p.TestCount = inCount
		if err := pgStore.UpsertProblem(ctx, &p); err != nil {
			log.Fatalf("Failed upserting %s to database: %v", p.ID, err)
		}

		importedCount++
		fmt.Printf("[%03d] Imported %-32s | %-24s | %d test vectors\n", p.DisplayOrder, p.Title, p.Category, inCount)
	}

	fmt.Printf("\nCompleted: %d problems imported into PostgreSQL and %s\n", importedCount, *problemsOut)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}