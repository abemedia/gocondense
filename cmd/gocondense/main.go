// Package main provides the gocondense command-line tool for condensing
// multi-line Go constructs into single-line equivalents where appropriate.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/semaphore"

	"github.com/abemedia/gocondense"
)

func main() { //nolint:funlen,gocognit
	var (
		maxLen   = flag.Int("max-len", 80, "Maximum line length before keeping multi-line")
		tabWidth = flag.Int("tab-width", 4, "Width of a tab character for line length calculation")
		help     = flag.Bool("help", false, "Show help message")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] [file|dir|path/...]", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nCondenses multi-line Go constructs into single-line constructs where appropriate.\n")
		fmt.Fprintf(os.Stderr, "If no file is provided, reads from stdin and writes to stdout.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *help {
		flag.Usage()
		return
	}

	formatter := gocondense.New(&gocondense.Config{
		MaxLen:   *maxLen,
		TabWidth: *tabWidth,
	})

	// For directory walks, skip generated files automatically.
	dirFormatter := gocondense.New(&gocondense.Config{
		MaxLen:        *maxLen,
		TabWidth:      *tabWidth,
		SkipGenerated: true,
	})

	if flag.NArg() == 0 {
		// Read from stdin
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading from stdin: %v\n", err)
			os.Exit(2)
		}
		output, err := formatter.Format(input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error formatting code: %v\n", err)
			os.Exit(2)
		}
		if _, err := os.Stdout.Write(output); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing to stdout: %v\n", err)
			os.Exit(2)
		}
		return
	}

	var (
		wg        sync.WaitGroup
		hasErrors atomic.Bool
	)
	sem := semaphore.NewWeighted(int64(runtime.NumCPU()))

	for _, path := range flag.Args() {
		arg := path
		recursive := strings.HasSuffix(path, "/...")
		if recursive {
			path = strings.TrimSuffix(path, "/...")
		}

		info, err := os.Stat(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error stating path %s: %v\n", path, err)
			hasErrors.Store(true)
			continue
		}

		f := formatter
		if info.IsDir() {
			f = dirFormatter
		}

		err = filepath.WalkDir(path, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".go") {
				_ = sem.Acquire(context.Background(), 1)
				wg.Add(1)
				go func(path string) {
					defer sem.Release(1)
					defer wg.Done()
					if !processFile(f, path) {
						hasErrors.Store(true)
					}
				}(path)
			} else if d.IsDir() && path != arg && (!recursive || filepath.Base(path) == "vendor") {
				return filepath.SkipDir
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error walking path %s: %v\n", path, err)
			hasErrors.Store(true)
		}
	}

	wg.Wait()

	if hasErrors.Load() {
		os.Exit(2)
	}
}

func processFile(formatter *gocondense.Formatter, filename string) bool {
	input, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file %s: %v\n", filename, err)
		return false
	}

	output, err := formatter.Format(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting file %s: %v\n", filename, err)
		return false
	}

	err = os.WriteFile(filename, output, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing file %s: %v\n", filename, err)
		return false
	}

	return true
}
