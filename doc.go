// Package gocondense provides a Go code formatter that condenses multi-line constructs
// into single-line equivalents where appropriate, improving code density while maintaining
// readability and respecting specified formatting constraints.
//
// The formatter can process various Go constructs including:
//   - Import declarations: Convert multi-line imports to single-line
//   - Function signatures: Condense parameter lists and return values
//   - Function literals: Compact anonymous function definitions
//   - Struct literals: Convert multi-line struct initialization to single-line
//   - Slice/array literals: Condense slice and array definitions
//   - Function calls: Compact multi-line function invocations
//   - Generic type parameters: Condense type parameter lists
//
// The package respects user-defined constraints such as maximum line length,
// maximum number of items per line, and feature-specific overrides. It preserves
// comments and only transforms constructs that are safe to condense without
// affecting code semantics or readability.
//
// Basic usage:
//
//	// Using default configuration
//	formatted, err := gocondense.Format(sourceCode)
//
//	// Using custom configuration
//	config := &gocondense.Config{
//		MaxLen:   120,
//		MaxItems: 5,
//		Enable:   gocondense.Funcs | gocondense.Calls,
//	}
//	formatter := gocondense.New(config)
//	formatted, err := formatter.Format(sourceCode)
//
// The formatter supports fine-grained control through feature flags and per-feature
// overrides, allowing users to customize behavior for specific construct types.
package gocondense
