// Package main provides the gocondense command-line tool for condensing
// multi-line Go constructs into single-line equivalents where appropriate.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sync/semaphore"

	"github.com/abemedia/gocondense"
)

var features = map[string]gocondense.Feature{
	"declarations": gocondense.Declarations,
	"types":        gocondense.Types,
	"funcs":        gocondense.Funcs,
	"literals":     gocondense.Literals,
	"calls":        gocondense.Calls,
	"structs":      gocondense.Structs,
	"slices":       gocondense.Slices,
	"maps":         gocondense.Maps,
	"all":          gocondense.All,
}

//nolint:cyclop,funlen,gocognit
func main() {
	var (
		maxLen   = flag.Int("max-len", 80, "Maximum line length before keeping multi-line")
		maxItems = flag.Int("max-items", 0, "Maximum number of items before keeping multi-line (0 for no limit)")
		tabWidth = flag.Int("tab-width", 4, "Width of a tab character for line length calculation")
		enable   = flag.String("enable", "all", "Comma-separated list of features to enable")
		disable  = flag.String("disable", "", "Comma-separated list of features to disable")
		help     = flag.Bool("help", false, "Show help message")
	)

	overrideMaxLen := make(map[string]*int)
	overrideMaxItems := make(map[string]*int)
	for name := range features {
		if name == "all" {
			continue
		}
		flagName := name + ".max-len"
		overrideMaxLen[name] = flag.Int(flagName, 0, "Override max-len for "+name)
		flagName = name + ".max-items"
		overrideMaxItems[name] = flag.Int(flagName, 0, "Override max-items for "+name)
	}

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] [file|dir|path/...]", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nCondenses multi-line Go constructs into single-line constructs where appropriate.\n")
		fmt.Fprintf(os.Stderr, "If no file is provided, reads from stdin and writes to stdout.\n\n")
		fmt.Fprintf(os.Stderr, "Available features: %s\n\n", strings.Join(slices.Sorted(maps.Keys(features)), ", "))
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *help {
		flag.Usage()
		return
	}

	enabled, err := parseFeatures(*enable)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing --enable flag: %v\n", err)
		os.Exit(1)
	}

	disabled, err := parseFeatures(*disable)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing --disable flag: %v\n", err)
		os.Exit(1)
	}

	overrides := make(map[gocondense.Feature]gocondense.ConfigOverride)
	for name, val := range overrideMaxLen {
		if *val > 0 {
			overrides[features[name]] = gocondense.ConfigOverride{MaxLen: *val}
		}
	}
	for name, val := range overrideMaxItems {
		if *val > 0 {
			override := overrides[features[name]]
			override.MaxItems = *val
			overrides[features[name]] = override
		}
	}

	config := &gocondense.Config{
		MaxLen:   *maxLen,
		MaxItems: *maxItems,
		TabWidth: *tabWidth,
		Enable:   enabled &^ disabled,
		Override: overrides,
	}

	formatter := gocondense.New(config)

	if flag.NArg() == 0 {
		// Read from stdin
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading from stdin: %v\n", err)
			os.Exit(1)
		}
		output, err := formatter.Format(input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error formatting code: %v\n", err)
			os.Exit(1)
		}
		os.Stdout.Write(output)
		return
	}

	var wg sync.WaitGroup
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
			continue
		}

		isDir := info.IsDir()

		err = filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && strings.HasSuffix(path, ".go") {
				if err := sem.Acquire(context.Background(), 1); err != nil {
					fmt.Fprintf(os.Stderr, "Failed to acquire semaphore: %v\n", err)
					return err
				}
				wg.Add(1)
				go func(path string) {
					defer sem.Release(1)
					defer wg.Done()
					processFile(formatter, path)
				}(path)
			} else if info.IsDir() && !recursive && isDir && path != arg {
				return filepath.SkipDir
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error walking path %s: %v\n", path, err)
			os.Exit(1)
		}
	}

	wg.Wait()
}

func parseFeatures(s string) (gocondense.Feature, error) {
	var f gocondense.Feature
	if s == "" {
		return f, nil
	}
	for part := range strings.SplitSeq(s, ",") {
		if feature, ok := features[strings.TrimSpace(part)]; ok {
			f |= feature
		} else {
			return 0, fmt.Errorf("unknown feature: %s", part)
		}
	}
	return f, nil
}

func processFile(formatter *gocondense.Formatter, filename string) {
	input, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file %s: %v\n", filename, err)
		return
	}

	output, err := formatter.Format(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting file %s: %v\n", filename, err)
		return
	}

	err = os.WriteFile(filename, output, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing file %s: %v\n", filename, err)
	}
}
