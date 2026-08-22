[![Go Doc][godoc-image]][godoc-url]

# file

`file` provides utilities and helper functions for file operations.

It includes a size-based age-based rotating file writer that is
a drop-in replacement for any file that needs to be bounded in size and age, such as log files.

## Quick Start

### Rotating

```go
package main

import (
  "log"
  "time"

  "github.com/neatplatform/mint/file"
)

func main() {
  f, err := file.NewRotating("/var/log/app/app.log", 10*1024*1024, 5, 7*24*time.Hour)
  if err != nil {
    log.Fatal(err)
  }
  defer f.Close()

  log.SetOutput(f)
  log.Println("Hello, World!")
}
```

`NewRotating` returns an `io.WriteCloser` that is safe for concurrent use.
It rotates the active file once it reaches `maxSize` bytes, renaming it to a timestamped backup.
Old backups are removed once they exceed `maxBackups` in count or `maxAge` in age.


[godoc-url]: https://pkg.go.dev/github.com/neatplatform/mint/file
[godoc-image]: https://pkg.go.dev/badge/github.com/neatplatform/mint/file
