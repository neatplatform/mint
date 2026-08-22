package file_test

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/neatplatform/mint/file"
)

func ExampleNewRotating() {
	dir, err := os.MkdirTemp("", "rotating-file-example")
	if err != nil {
		log.Fatalf("Error on creating temp directory: %s", err)
	}

	defer func() {
		_ = os.RemoveAll(dir)
	}()

	w, err := file.NewRotating(filepath.Join(dir, "app.log"), 1*1024*1024, 5, 7*24*time.Hour)
	if err != nil {
		log.Fatalf("Error on creating a new rotating file: %s", err)
	}

	defer func() {
		_ = w.Close()
	}()

	n, err := w.Write([]byte("Hello, World!\n"))
	if err != nil {
		log.Fatalf("Error on writing to the file: %s", err)
	}

	fmt.Printf("%d\n", n)
}
