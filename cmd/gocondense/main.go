// Package main provides the gocondense command-line tool for condensing
// multi-line Go constructs into single-line equivalents where appropriate.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	goformat "go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/mod/modfile"
	"golang.org/x/sync/semaphore"

	"github.com/abemedia/gocondense"
)

const (
	parserMode = parser.ParseComments | parser.SkipObjectResolution

	// normalizeNumbers is an unexported printer mode that canonicalises
	// number-literal prefixes and exponents (1 << 30 since Go 1.13).
	// TestNormalizeNumbers guards against this value changing.
	normalizeNumbers = 1 << 30

	printerMode = printer.UseSpaces | printer.TabIndent | normalizeNumbers
)

var printCfg = printer.Config{Mode: printerMode, Tabwidth: 8}

func main() {
	os.Exit(run(os.Args, os.Stdin, os.Stdout, os.Stderr))
}

// run parses flags and dispatches to stdin formatting or file processing.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)

	maxLen := flags.Int("max-len", 80, "maximum line length before keeping multi-line")
	tabWidth := flags.Int("tab-width", 4, "width of a tab character for line length calculation")
	listOnly := flags.Bool("l", false, "list files whose formatting differs")
	showDiff := flags.Bool("d", false, "display diffs instead of rewriting files")

	flags.Usage = func() {
		fmt.Fprintf(stderr, "Usage: %s [options] [file|dir|path/...]", args[0])
		fmt.Fprintf(stderr, "\nCondenses multi-line Go constructs into single-line constructs where appropriate.\n")
		fmt.Fprintf(stderr, "If no file is provided, reads from stdin and writes to stdout.\n\n")
		fmt.Fprintf(stderr, "Options:\n")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *maxLen < 0 || *tabWidth < 0 {
		fmt.Fprintf(stderr, "max-len and tab-width must not be negative\n")
		flags.Usage()
		return 2
	}

	formatter := gocondense.New(gocondense.Config{
		MaxLen:   *maxLen,
		TabWidth: *tabWidth,
	})

	if flags.NArg() == 0 {
		return formatStdin(formatter, stdin, stdout, stderr)
	}
	return processArgs(formatter, flags.Args(), stdout, stderr, *listOnly, *showDiff)
}

// formatStdin reads Go source from stdin, formats it, and writes to stdout.
func formatStdin(formatter *gocondense.Formatter, stdin io.Reader, stdout, stderr io.Writer) int {
	input, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "Error reading stdin: %v\n", err)
		return 2
	}

	fset := token.NewFileSet()
	file, sourceAdj, indentAdj, err := parse(fset, "<standard input>", input, true)
	if err != nil {
		fmt.Fprintf(stderr, "Error parsing stdin: %v\n", err)
		return 2
	}

	formatter.File(fset, file)

	if sourceAdj == nil {
		// Complete file (not fragment) - sort imports as a final step.
		ast.SortImports(fset, file)
	}

	output, err := format(fset, file, sourceAdj, indentAdj, input, printCfg)
	if err != nil {
		fmt.Fprintf(stderr, "Error formatting stdin: %v\n", err)
		return 2
	}

	if _, err := stdout.Write(output); err != nil {
		fmt.Fprintf(stderr, "Error writing stdout: %v\n", err)
		return 2
	}
	return 0
}

// processArgs formats the given file and directory arguments concurrently.
func processArgs( //nolint:funlen
	formatter *gocondense.Formatter,
	args []string,
	stdout, stderr io.Writer,
	listOnly, showDiff bool,
) int {
	var (
		wg         sync.WaitGroup
		hasErrors  atomic.Bool
		hasChanged atomic.Bool
		sem        = semaphore.NewWeighted(int64(runtime.NumCPU()))
	)

	for _, arg := range args {
		root, recursive := strings.CutSuffix(arg, "/...")
		if recursive && root == "" {
			root = "/" // Avoid empty root when arg is "/..."
		}
		root = filepath.Clean(root)
		info, err := os.Stat(root)
		if err != nil {
			fmt.Fprintf(stderr, "Error stating path %s: %v\n", root, err)
			hasErrors.Store(true)
			continue
		}

		// Skip generated files automatically for directory walks.
		skipGenerated := info.IsDir()

		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case d.IsDir():
				if p != root && (!recursive || shouldIgnore(p)) {
					return filepath.SkipDir
				}
			case p == root, strings.HasSuffix(d.Name(), ".go") && !strings.HasPrefix(d.Name(), "."):
				_ = sem.Acquire(context.Background(), 1)
				wg.Add(1)
				go func() {
					defer sem.Release(1)
					defer wg.Done()
					switch processFile(formatter, p, skipGenerated, stdout, stderr, listOnly, showDiff) {
					case resultError:
						hasErrors.Store(true)
					case resultChanged:
						hasChanged.Store(true)
					}
				}()
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(stderr, "Error reading path %s: %v\n", root, err)
			hasErrors.Store(true)
		}
	}
	wg.Wait()
	if hasErrors.Load() {
		return 2
	}
	if (listOnly || showDiff) && hasChanged.Load() {
		return 1
	}
	return 0
}

type result int

const (
	resultOK      result = iota // no changes needed or file written successfully
	resultChanged               // file differs (used with -l/-d)
	resultError                 // an error occurred
)

// processFile reads, formats, and writes back a single Go file.
func processFile(
	formatter *gocondense.Formatter,
	filename string,
	skipGenerated bool,
	stdout, stderr io.Writer,
	listOnly, showDiff bool,
) result {
	input, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(stderr, "Error reading file %s: %v\n", filename, err)
		return resultError
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, input, parserMode)
	if err != nil {
		fmt.Fprintf(stderr, "Error parsing file %s: %v\n", filename, err)
		return resultError
	}

	if skipGenerated && ast.IsGenerated(file) {
		return resultOK
	}

	formatter.File(fset, file)

	var buf bytes.Buffer
	if err := goformat.Node(&buf, fset, file); err != nil {
		fmt.Fprintf(stderr, "Error formatting file %s: %v\n", filename, err)
		return resultError
	}
	output := buf.Bytes()

	if bytes.Equal(input, output) {
		return resultOK
	}

	if listOnly {
		fmt.Fprintln(stdout, filename)
		return resultChanged
	}
	if showDiff {
		fmt.Fprint(stdout, unifiedDiff(filename, input, output))
		return resultChanged
	}

	err = os.WriteFile(filename, output, 0o600)
	if err != nil {
		fmt.Fprintf(stderr, "Error writing file %s: %v\n", filename, err)
		return resultError
	}

	return resultOK
}

// shouldIgnore reports whether dir should be skipped.
func shouldIgnore(dir string) bool {
	switch filepath.Base(dir) {
	case "vendor", "testdata":
		return true
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	root, paths := loadModIgnore(abs)
	if len(paths) == 0 {
		return false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	for _, path := range paths {
		p, rooted := strings.CutPrefix(path, "./")
		p = strings.Trim(p, "/")
		if rel == p || (!rooted && strings.HasSuffix(rel, "/"+p)) {
			return true
		}
	}
	return false
}

type modIgnore struct {
	dir   string
	paths []string
}

// modIgnoreCache caches go.mod ignore directives by directory path.
// Safe without synchronization: only accessed from the sequential WalkDir callback.
var modIgnoreCache = map[string]modIgnore{}

// loadModIgnore returns the go.mod ignore directives for dir.
func loadModIgnore(dir string) (root string, paths []string) {
	if ig, ok := modIgnoreCache[dir]; ok {
		return ig.dir, ig.paths
	}

	root, paths = func() (string, []string) {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return "", nil
			}
			if parent := filepath.Dir(dir); parent != dir {
				return loadModIgnore(parent)
			}
			return "", nil
		}
		f, err := modfile.ParseLax("go.mod", data, nil)
		if err != nil {
			return "", nil
		}
		ignore := make([]string, len(f.Ignore))
		for i, ig := range f.Ignore {
			ignore[i] = ig.Path
		}
		return dir, ignore
	}()

	modIgnoreCache[dir] = modIgnore{root, paths}
	return root, paths
}
