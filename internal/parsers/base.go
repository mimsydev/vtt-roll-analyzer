package parsers

import (
	"fmt"
)

// A general type for any of the parser implementations
type ParserFunc func(dir string) (string, error)

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
