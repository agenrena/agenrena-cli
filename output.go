package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

type okEnvelope struct {
	OK       bool        `json:"ok"`
	Data     any         `json:"data,omitempty"`
	Warnings []string    `json:"warnings,omitempty"`
	Meta     interface{} `json:"meta,omitempty"`
}

type errorEnvelope struct {
	OK    bool `json:"ok"`
	Error struct {
		Code        string `json:"code"`
		Message     string `json:"message"`
		Recoverable bool   `json:"recoverable"`
	} `json:"error"`
}

func writeOK(data any) error {
	return writeJSON(os.Stdout, okEnvelope{OK: true, Data: data})
}

func writeOKWithWarnings(data any, warnings []string) error {
	return writeJSON(os.Stdout, okEnvelope{OK: true, Data: data, Warnings: warnings})
}

func writeErrorAndExit(err error) {
	var ce *cliError
	if !errors.As(err, &ce) {
		ce = &cliError{Code: "INTERNAL_ERROR", Message: err.Error()}
	}

	env := errorEnvelope{OK: false}
	env.Error.Code = ce.Code
	env.Error.Message = ce.Message
	env.Error.Recoverable = ce.Recoverable
	if encErr := writeJSON(os.Stdout, env); encErr != nil {
		fmt.Fprintln(os.Stderr, encErr)
	}
	os.Exit(1)
}

func writeJSON(out *os.File, value any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}
