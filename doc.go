// Package gocondense provides a Go code formatter that condenses multi-line constructs
// into single lines where appropriate, improving code density while maintaining
// readability and respecting specified formatting constraints.
//
// The formatter can process various Go constructs including:
//   - Declaration groups: Convert multi-line single-item declarations to single-line
//   - Function signatures: Condense parameter lists and return values
//   - Function literals: Compact anonymous function definitions
//   - Struct literals: Convert multi-line struct initialization to single-line
//   - Slice/array literals: Condense slice and array definitions
//   - Function calls: Compact multi-line function invocations
//   - Generic type parameters: Condense type parameter lists
//
// The package respects user-defined constraints such as maximum line length,
// maximum number of key-value pairs, and feature-specific controls. It preserves
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
//		MaxLen:      120,
//		MaxKeyValue: 5,
//		Enable:      gocondense.Funcs | gocondense.Calls,
//	}
//	formatter := gocondense.New(config)
//	formatted, err := formatter.Format(sourceCode)
//
// The formatter supports fine-grained control through feature flags,
// allowing users to enable or disable specific formatting behaviors.
package gocondense
