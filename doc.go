// Package gocondense provides a Go code formatter that condenses multi-line
// constructs onto single lines where they fit, reducing vertical noise while
// preserving readability.
//
// Format a source file with default settings (80 columns, 4-wide tabs):
//
//	formatted, err := gocondense.Format(src)
//
// Use a custom configuration:
//
//	f := gocondense.New(&gocondense.Config{
//		MaxLen:   120,
//		TabWidth: 2,
//	})
//	formatted, err := f.Format(src)
package gocondense
