package main

import (
	"flag"
	"strings"

	"github.com/mimsydev/vtt-roll-analyzer/internal/parsers"
)

var parserType string

func init() {
	const (
		defaultParser = "roll20"
		parserUsage   = "indicates which parser shoule be used for the " +
			"provided files"
	)
	flag.StringVar(&parserType, "parser", defaultParser, parserUsage)
	flag.StringVar(&parserType, "p", defaultParser, parserUsage+" (shorthand)")
}

func main() {
	flag.Parse()
	parserType = strings.TrimSpace(parserType)

	parseFiles, err := parsers.GetParser(parserType)
	if err != nil {
		println(err.Error())
		return
	}
	_, err = parseFiles("This is a string")
	if err != nil {
		println(err.Error())
	}
}
