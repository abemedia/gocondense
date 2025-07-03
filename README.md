# gocondense

<img align="right" width="200" alt="" src="assets/logo.png">

[![Go Reference](https://pkg.go.dev/badge/github.com/abemedia/gocondense.svg)](https://pkg.go.dev/github.com/abemedia/gocondense)
[![Codecov](https://codecov.io/gh/abemedia/gocondense/branch/master/graph/badge.svg)](https://codecov.io/gh/abemedia/gocondense)
[![CI](https://github.com/abemedia/gocondense/actions/workflows/test.yml/badge.svg)](https://github.com/abemedia/gocondense/actions/workflows/test.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/abemedia/gocondense)](https://goreportcard.com/report/github.com/abemedia/gocondense)

A configurable Go code formatter that condenses multi-line constructs into single-line constructs where appropriate, improving code density while preserving readability, comments, and semantics.

## Features

- **Configurable**: Fine-grained control over which constructs to condense
- **Flexible Limits**: Set maximum line length and item count globally or per-feature
- **Preserves Comments**: All comments are preserved in their original positions
- **Semantic Safety**: No changes to code semantics or behavior
- **CLI and Library**: Available as both a command-line tool and Go library

## Installation

### Command Line Tool

```bash
go install github.com/abemedia/gocondense/cmd/gocondense@latest
```

### Library

```bash
go get github.com/abemedia/gocondense
```

## Supported Constructs

### Declaration Groups

Condenses single-item declaration groups (import, var, const, type) from multi-line to single-line format.

**Before:**

```go
import (
    "fmt"
)

var (
    x = 1
)

const (
    Name = "value"
)

type (
    ID int
)
```

**After:**

```go
import "fmt"

var x = 1

const Name = "value"

type ID int
```

---

### Function Declarations

Condenses function parameters and return values.

**Before:**

```go
func Add(
    a int,
    b int,
) (
    result int,
    err error,
) {
    return a + b, nil
}
```

**After:**

```go
func Add(a int, b int) (result int, err error) {
    return a + b, nil
}
```

**Supports:**

- Regular function parameters
- Named return values
- Generic type parameters
- Variadic functions

---

### Function Literals (Anonymous Functions)

Condenses function literal signatures.

**Before:**

```go
callback := func(
    x int,
    y int,
) int {
    return x + y
}
```

**After:**

```go
callback := func(x int, y int) int {
    return x + y
}
```

---

### Function Calls

Condenses function call arguments.

**Before:**

```go
result := myFunction(
    arg1,
    arg2,
    arg3,
)

fmt.Printf(
    "Hello %s, you are %d years old",
    name,
    age,
)
```

**After:**

```go
result := myFunction(arg1, arg2, arg3)

fmt.Printf("Hello %s, you are %d years old", name, age)
```

**Supports:**

- Regular function calls
- Method calls
- Variadic arguments (with `...`)

---

### Struct Literals

Condenses struct initialization with named fields.

**Before:**

```go
person := Person{
    Name: "John",
    Age:  30,
    City: "New York",
}
```

**After:**

```go
person := Person{Name: "John", Age: 30, City: "New York"}
```

---

### Slice and Array Literals

Condenses slice and array definitions.

**Before:**

```go
numbers := []int{
    1,
    2,
    3,
    4,
}

fruits := []string{
    "apple",
    "banana",
    "cherry",
}
```

**After:**

```go
numbers := []int{1, 2, 3, 4}

fruits := []string{"apple", "banana", "cherry"}
```

---

### Map Literals

Condenses map initialization.

**Before:**

```go
config := map[string]int{
    "apple":  1,
    "banana": 2,
    "cherry": 3,
}
```

**After:**

```go
config := map[string]int{"apple": 1, "banana": 2, "cherry": 3}
```

---

### Generic Type Parameters

Condenses generic type parameter lists and instantiations.

**Before:**

```go
func GenericFunc[
    T any,
    U comparable,
](
    a T,
    b U,
) T {
    return a
}

// Type instantiation
var result = GenericFunc[
    string,
    int,
]("hello", 42)
```

**After:**

```go
func GenericFunc[T any, U comparable](a T, b U) T {
    return a
}

// Type instantiation
var result = GenericFunc[string, int]("hello", 42)
```

## Command Line Tool

### Basic Usage

Format a single file (modifies in-place):

```bash
gocondense myfile.go
```

Format multiple files:

```bash
gocondense file1.go file2.go file3.go
```

Format all Go files in a directory:

```bash
gocondense ./
```

Format all Go files recursively:

```bash
gocondense ./...
```

Format from stdin to stdout:

```bash
cat myfile.go | gocondense
```

### Configuration Options

#### Global Settings

| Flag          | Description                                       | Default      |
| ------------- | ------------------------------------------------- | ------------ |
| `--max-len`   | Maximum line length before keeping multi-line     | 80           |
| `--max-items` | Maximum number of items before keeping multi-line | 0 (no limit) |
| `--tab-width` | Width of tab character for length calculation     | 4            |
| `--enable`    | Comma-separated list of features to enable        | "all"        |
| `--disable`   | Comma-separated list of features to disable       | ""           |

#### Feature-Specific Overrides

You can override global settings for specific features:

| Flag                       | Description                                  |
| -------------------------- | -------------------------------------------- |
| `--declarations.max-len`   | Override max-len for declarations            |
| `--declarations.max-items` | Override max-items for declarations          |
| `--types.max-len`          | Override max-len for type parameters         |
| `--types.max-items`        | Override max-items for type parameters       |
| `--funcs.max-len`          | Override max-len for function declarations   |
| `--funcs.max-items`        | Override max-items for function declarations |
| `--literals.max-len`       | Override max-len for function literals       |
| `--literals.max-items`     | Override max-items for function literals     |
| `--calls.max-len`          | Override max-len for function calls          |
| `--calls.max-items`        | Override max-items for function calls        |
| `--structs.max-len`        | Override max-len for struct literals         |
| `--structs.max-items`      | Override max-items for struct literals       |
| `--slices.max-len`         | Override max-len for slice literals          |
| `--slices.max-items`       | Override max-items for slice literals        |
| `--maps.max-len`           | Override max-len for map literals            |
| `--maps.max-items`         | Override max-items for map literals          |

### Examples

**Limit line length to 120 characters:**

```bash
gocondense --max-len 120 myfile.go
```

**Allow maximum 3 items per line:**

```bash
gocondense --max-items 3 myfile.go
```

**Only condense function calls and struct literals:**

```bash
gocondense --enable calls,structs myfile.go
```

**Condense everything except declarations:**

```bash
gocondense --disable declarations myfile.go
```

**Allow more items for function calls than other constructs:**

```bash
gocondense --max-items 2 --calls.max-items 5 myfile.go
```

**Use longer lines for struct literals:**

```bash
gocondense --max-len 80 --structs.max-len 120 myfile.go
```

### Available Features

- `declarations` - Declaration groups (import, var, const, type)
- `types` - Type parameters and instantiations
- `funcs` - Function declarations
- `literals` - Function literals
- `calls` - Function calls
- `structs` - Struct literals
- `slices` - Slice and array literals
- `maps` - Map literals
- `all` - All features combined

## Go Library

### Basic Usage

```go
package main

import (
    "fmt"
    "log"

    "github.com/abemedia/gocondense"
)

func main() {
    sourceCode := []byte(`
func add(
    a int,
    b int,
) int {
    return a + b
}`)

    // Using default configuration
    formatted, err := gocondense.Format(sourceCode)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(string(formatted))
    // Output: func add(a int, b int) int { return a + b }
}
```

### Custom Configuration

```go
package main

import (
    "fmt"
    "log"

    "github.com/abemedia/gocondense"
)

func main() {
    config := &gocondense.Config{
        MaxLen:   120,            // Allow longer lines
        MaxItems: 5,              // Allow up to 5 items per line
        TabWidth: 4,              // Tab width for length calculation
        Enable:   gocondense.All, // Enable all features
    }

    formatter := gocondense.New(config)
    formatted, err := formatter.Format(sourceCode)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(string(formatted))
}
```

### Feature Selection

```go
// Enable only specific features
config := &gocondense.Config{
    MaxLen: 80,
    Enable: gocondense.Funcs | gocondense.Calls, // Only functions and calls
}

// Enable all except declarations
config := &gocondense.Config{
    MaxLen: 80,
    Enable: gocondense.All &^ gocondense.Declarations, // All except declarations
}
```

### Per-Feature Overrides

```go
config := &gocondense.Config{
    MaxLen:   80,  // Global limit
    MaxItems: 3,   // Global item limit
    Enable:   gocondense.All,
    Override: map[gocondense.Feature]gocondense.ConfigOverride{
        gocondense.Calls: {
            MaxLen:   120, // Allow longer lines for function calls
            MaxItems: 6,   // Allow more arguments for function calls
        },
        gocondense.Structs: {
            MaxItems: 2,   // Be more conservative with struct fields
        },
    },
}
```
