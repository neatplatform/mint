[![Go Doc][godoc-image]][godoc-url]

# factory

`factory` is a helper package for tests and fixtures.
It provides functions to generate random values for Go built-in types
and can populate user-defined structs through reflection.

## Quick Start

```go
package main

import (
  "fmt"

  "github.com/neatplatform/mint/factory"
)

func main() {
  name := factory.Name()
  email := factory.Email()

  fmt.Printf("%s <%s>\n", name, email)
}
```

```go
package main

import (
  "fmt"
  "log"
  "net/url"
  "time"

  "github.com/neatplatform/mint/factory"
)

func main() {
  object := struct {
    String     string
    Bool       bool
    Int        int
    Uint       uint
    Float64    float64
    Complex128 complex128
    Nested     struct {
      Duration time.Duration
      Time     *time.Time
      URL      *url.URL
    }
  }{}

  if err := factory.Populate(&object, false); err != nil {
    log.Fatalf("Error on populating: %s", err)
  }

  fmt.Printf("%+v\n", object)
}
```


[godoc-url]: https://pkg.go.dev/github.com/neatplatform/mint/factory
[godoc-image]: https://pkg.go.dev/badge/github.com/neatplatform/mint/factory
