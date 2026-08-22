package ptr_test

import (
	"fmt"
	"time"

	"github.com/neatplatform/mint/ptr"
)

func ExampleString() {
	p := ptr.String("Hello, World!")
	fmt.Printf("%v\n", p)
}

func ExampleBool() {
	p := ptr.Bool(true)
	fmt.Printf("%v\n", p)
}

func ExampleInt() {
	p := ptr.Int(-1)
	fmt.Printf("%v\n", p)
}

func ExampleFloat64() {
	p := ptr.Float64(3.1415926535)
	fmt.Printf("%v\n", p)
}

func ExampleComplex64() {
	p := ptr.Complex64(0.5 + 14.134725i)
	fmt.Printf("%v\n", p)
}

func ExampleUint() {
	p := ptr.Uint(23)
	fmt.Printf("%v\n", p)
}

func ExampleRune() {
	p := ptr.Rune('M')
	fmt.Printf("%v\n", p)
}

func ExampleDuration() {
	p := ptr.Duration(30 * time.Minute)
	fmt.Printf("%v\n", p)
}
