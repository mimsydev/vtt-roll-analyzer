package main

import (
	"fmt"
	// "errors"
	"github.com/mimsydev/vtt-roll-analyzer/internal/reader"
)

func main() {
	i := 1
	fmt.Printf("printing %d\n", i)
	path, er := reader.ReadFiles()
	if er != nil {
		println("Exception happened")
	}
	println(path)
}
