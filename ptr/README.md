[![Go Doc][godoc-image]][godoc-url]

# ptr

`ptr` provides small helper functions for creating **pointers** to literal values in Go.
It includes typed constructors for common built-in types, making struct initialization and optional fields cleaner.

## Quick Start

```go
import (
  "fmt"

  "github.com/neatplatform/mint/ptr"
)

func ExampleString() {
  p := ptr.String("Hello, World!")
  fmt.Printf("%v\n", p)
}
```


[godoc-url]: https://pkg.go.dev/github.com/neatplatform/mint/ptr
[godoc-image]: https://pkg.go.dev/badge/github.com/neatplatform/mint/ptr
