[![Go Doc][godoc-image]][godoc-url]

# config

config is a lightweight, unopinionated Go package for loading application configuration.

Define your configuration once as a `struct` with typed fields and sensible defaults,
then let `config` populate it from familiar sources:

  1. **Command-line flags**
  2. **Environment variables**
  3. **Configuration files**

**Need runtime updates?**

`config` can watch configuration files for changes and notify subscribers,
so your application can react without a restart.

`config` also does not rely on Go's `flag` package for parsing,
which means you are free to parse your own flags however you prefer.

## Quick Start

You can find examples [here](./examples).

| Example | Description |
|---------|-------------|
| Basic | Using `Pick` method. |
| Watch | Watching for new config changes. |
| Kubernetes | Changing the *log level* without restarting the *pods*. |
| Telepresence | Running a service in a [Telepresence](https://telepresence.io) session. |

## Supported Types

  - `string`, `*string`, `[]string`
  - `bool`, `*bool`, `[]bool`
  - `int`, `int8`, `int16`, `int32`, `int64`
  - `*int`, `*int8`, `*int16`, `*int32`, `*int64`
  - `[]int`, `[]int8`, `[]int16`, `[]int32`, `[]int64`
  - `uint`, `uint8`, `uint16`, `uint32`, `uint64`
  - `*uint`, `*uint8`, `*uint16`, `*uint32`, `*uint64`
  - `[]uint`, `[]uint8`, `[]uint16`, `[]uint32`, `[]uint64`
  - `float32`, `float64`
  - `*float32`, `*float64`
  - `[]float32`, `[]float64`
  - `byte`, `*byte`, `[]byte`
  - `rune`, `*rune`, `[]rune`
  - `url.URL`, `*url.URL`, `[]url.URL`
  - `time.Duration`, `*time.Duration`, `[]time.Duration`
  - `regexp.Regexp`, `*regexp.Regexp`, `[]regexp.Regexp`

## Expected Behavior

Configuration values are resolved in the following order (highest to lowest priority):

  1. Command-line flags
  2. Environment variables
  3. Configuration files
  4. Default values (defined on your struct instance)

You can provide values through **flags** using any of these formats:

```bash
app  -enabled  -log.level info  -timeout 30s  -address http://localhost:8080  -endpoints url1,url2,url3
app  -enabled  -log.level=info  -timeout=30s  -address=http://localhost:8080  -endpoints=url1,url2,url3
app --enabled --log.level info --timeout 30s --address http://localhost:8080 --endpoints url1,url2,url3
app --enabled --log.level=info --timeout=30s --address=http://localhost:8080 --endpoints=url1,url2,url3
```

You can also provide values through **environment variables**:

```bash
export ENABLED=true
export LOG_LEVEL=info
export TIMEOUT=30s
export ADDRESS=http://localhost:8080
export ENDPOINTS=url1,url2,url3
```

You can also store values in **files** (for example, mounted config files or secrets)
and pass file paths through environment variables:

```bash
export ENABLED_FILE=...
export LOG_LEVEL_FILE=...
export TIMEOUT_FILE=...
export ADDRESS_FILE=...
export ENDPOINTS_FILE=...
```

## Features

### Skipping

To disable a source for a field, set its tag to `-`:

```go
type Config struct {
  GithubToken string `env:"-" fileenv:"-"`
}
```

In this example, `GithubToken` is ignored for environment variable and file-based loading,
so it can only be populated from the `github.token` command-line flag (or from its default value on the struct).

### Customization

Use Go *struct tags* to override the default names for flags, environment variables, and configuration files.

```go
type Config struct {
  Database string `flag:"config.database" env:"CONFIG_DATABASE" fileenv:"CONFIG_DATABASE_FILE_PATH"`
}
```

In this example, `Database` can be populated from:

  1. The command-line flag `config.database`
  2. The environment variable `CONFIG_DATABASE`
  3. The file path in `CONFIG_DATABASE_FILE_PATH`
  4. The default value defined on the struct instance

### Using `flag` Package

`config` works seamlessly with Go's built-in `flag` package
because it does not rely on `flag` to parse command-line arguments.
This means you can define, parse, and use your own flags exactly as usual.

When you use the `flag` package, `config` also registers the configuration flags it expects.

```go
package main

import (
  "flag"
  "time"

  "github.com/neatplatform/mint/config"
)

var config = struct {
  Enabled   bool
  LogLevel  string
} {
  Enabled:  true,   // default
  LogLevel: "info", // default
}

func main() {
  config.Pick(&config)
  flag.Parse()
}
```

If you run your program with `-help` or `--help`,
you will also see flags such as `-enabled` and `-log.level` with their descriptions.

### Options

Options let you adjust how `config` behaves for specific setups and use cases.
You can pass them directly to `Pick` and `Watch`.

For testing, debugging, or temporary overrides,
many of these options can also be configured through environment variables without changing your code.

| Option | Environment Variable | Description |
|--------|----------------------|-------------|
| `config.Debug()` | `CONFIG_DEBUG` | Showing debugging logs. |
| `config.ListSep()` | `CONFIG_LIST_SEP` | Specifying list separator for all fields with slice type. |
| `config.SkipFlag()` | `CONFIG_SKIP_FLAG` | Skipping command-line flags for all fields. |
| `config.SkipEnv()` | `CONFIG_SKIP_ENV` | Skipping environment variables for all fields .|
| `config.SkipFileEnv()` | `CONFIG_SKIP_FILE_ENV` | Skipping file environment variables (and configuration files) for all fields. |
| `config.PrefixFlag()` | `CONFIG_PREFIX_FLAG` | Prefixing all flag names with a string. |
| `config.PrefixEnv()` | `CONFIG_PREFIX_ENV` | Prefixing all environment variable names with a string. |
| `config.PrefixFileEnv()` | `CONFIG_PREFIX_FILE_ENV` | Prefixing all file environment variable names with a string. |
| `config.Telepresence()` | `CONFIG_TELEPRESENCE` | Reading configuration files in a *Telepresence* environment. |

#### Debugging

If configuration is not being loaded as expected, enable debug logging to inspect how `config` resolves values.
You can enable it either with the `Debug` option or by setting the `CONFIG_DEBUG` environment variable.
Both approaches require a verbosity level.

| Level | Descriptions                                               |
|-------|------------------------------------------------------------|
| `0`   | No logging (default).                                      |
| `1`   | Logging all errors.                                        |
| `2`   | Logging initialization information.                        |
| `3`   | Logging information related to new values read from files. |
| `4`   | Logging information related to notifying subscribers.      |
| `5`   | Logging information related to setting values of fields.   |
| `6`   | Logging miscellaneous information.                         |

#### Watching

`config` can watch configuration files and update values while your application is running.

When you use `Watch()`, your struct should include a `sync.Mutex` field to keep updates synchronized and avoid data races.
You can find a basic `Watch()` example [here](./examples/2-watch).

For a real-world example, see [this post](https://milad.dev/posts/dynamic-config-secret) on using `config.Watch()`
for **dynamic configuration management** and **secret injection** in Go applications running on Kubernetes.


[godoc-url]: https://pkg.go.dev/github.com/neatplatform/mint/config
[godoc-image]: https://pkg.go.dev/badge/github.com/neatplatform/mint/config
