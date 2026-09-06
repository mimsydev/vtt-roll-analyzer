package parsers

import (
	"fmt"
)

// A general interface for any of the parser implementations
type LogParser interface {
	Parse(dir string) (string, error)
}

type ParserFunc func(dir string) (string, error)

func (pf ParserFunc) Parse(dir string) (string, error) {
	return pf(dir)
}

// A registry for parsers
var parserMap = map[string]ParserFunc{
	"roll20":  parseRoll20,
	"foundry": parseFoundry,
}

func GetParser(parserName string) (ParserFunc, error) {
	retParser, ok := parserMap[parserName]
	if !ok {
		return nil, fmt.Errorf("%v is not a valid parser", parserName)
	}
	return retParser, nil
}
