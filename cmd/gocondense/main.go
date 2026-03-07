// Package main provides the gocondense command-line tool for condensing
// multi-line Go constructs into single-line equivalents where appropriate.
package main

import (
	"bytes"
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

func main() {
	os.Exit(run(os.Args, os.Stdin, os.Stdout, os.Stderr))
}

// run parses flags and dispatches to stdin formatting or file processing.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)

	maxLen := flags.Int("max-len", 80, "Maximum line length before keeping multi-line")
	tabWidth := flags.Int("tab-width", 4, "Width of a tab character for line length calculation")
	help := flags.Bool("help", false, "Show help message")

	flags.Usage = func() {
		fmt.Fprintf(stderr, "Usage: %s [options] [file|dir|path/...]", args[0])
		fmt.Fprintf(stderr, "\nCondenses multi-line Go constructs into single-line constructs where appropriate.\n")
		fmt.Fprintf(stderr, "If no file is provided, reads from stdin and writes to stdout.\n\n")
		fmt.Fprintf(stderr, "Options:\n")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}

	if *help {
		flags.Usage()
		return 0
	}

	if *maxLen < 0 || *tabWidth < 0 {
		fmt.Fprintf(stderr, "max-len and tab-width must not be negative\n")
		flags.Usage()
		return 2
	}

	cfg := &gocondense.Config{
		MaxLen:   *maxLen,
		TabWidth: *tabWidth,
	}

	if flags.NArg() == 0 {
		return formatStdin(cfg, stdin, stdout, stderr)
	}
	return processArgs(cfg, flags.Args(), stderr)
}

// formatStdin reads Go source from stdin, formats it, and writes to stdout.
func formatStdin(cfg *gocondense.Config, stdin io.Reader, stdout, stderr io.Writer) int {
	input, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "Error reading from stdin: %v\n", err)
		return 2
	}
	output, err := gocondense.New(cfg).Format(input)
	if err != nil {
		fmt.Fprintf(stderr, "Error formatting code: %v\n", err)
		return 2
	}
	if _, err := stdout.Write(output); err != nil {
		fmt.Fprintf(stderr, "Error writing to stdout: %v\n", err)
		return 2
	}
	return 0
}

// processArgs formats the given file and directory arguments concurrently.
func processArgs(cfg *gocondense.Config, args []string, stderr io.Writer) int {
	formatter := gocondense.New(cfg)

	// For directory walks, skip generated files automatically.
	dirCfg := *cfg
	dirCfg.SkipGenerated = true
	dirFormatter := gocondense.New(&dirCfg)

	var (
		wg        sync.WaitGroup
		hasErrors atomic.Bool
		sem       = semaphore.NewWeighted(int64(runtime.NumCPU()))
	)

	for _, arg := range args {
		root, recursive := strings.CutSuffix(arg, "/...")
		info, err := os.Stat(root)
		if err != nil {
			fmt.Fprintf(stderr, "Error stating path %s: %v\n", root, err)
			hasErrors.Store(true)
			continue
		}

		f := formatter
		if info.IsDir() {
			f = dirFormatter
		}

		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, ".go") {
				_ = sem.Acquire(context.Background(), 1)
				wg.Add(1)
				go func() {
					defer sem.Release(1)
					defer wg.Done()
					if !processFile(f, p, stderr) {
						hasErrors.Store(true)
					}
				}()
			} else if d.IsDir() && p != root && (!recursive || filepath.Base(p) == "vendor") {
				return filepath.SkipDir
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(stderr, "Error walking path %s: %v\n", root, err)
			hasErrors.Store(true)
		}
	}

	wg.Wait()

	if hasErrors.Load() {
		return 2
	}
	return 0
}

// processFile reads, formats, and writes back a single Go file.
func processFile(formatter *gocondense.Formatter, filename string, stderr io.Writer) bool {
	input, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(stderr, "Error reading file %s: %v\n", filename, err)
		return false
	}

	output, err := formatter.Format(input)
	if err != nil {
		fmt.Fprintf(stderr, "Error formatting file %s: %v\n", filename, err)
		return false
	}

	if bytes.Equal(input, output) {
		return true
	}

	err = os.WriteFile(filename, output, 0o600)
	if err != nil {
		fmt.Fprintf(stderr, "Error writing file %s: %v\n", filename, err)
		return false
	}

	return true
}
