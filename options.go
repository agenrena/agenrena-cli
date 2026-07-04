package main

import (
	"fmt"
	"strconv"
)

func requiredOptionValue(args []string, index int) (string, int, error) {
	if index+1 >= len(args) {
		return "", index, usageError(args[index] + " requires a value")
	}
	return args[index+1], index + 1, nil
}

func parseIntOption(name, value string, minimum int) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum {
		return 0, usageError(fmt.Sprintf("%s must be an integer greater than or equal to %d", name, minimum))
	}
	return parsed, nil
}
