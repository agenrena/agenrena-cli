package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

func runArena(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing arena command")
	}
	switch args[0] {
	case "slots":
		return arenaSlots(ctx, args[1:])
	case "submit":
		return arenaSubmit(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown arena command %q", args[0]))
	}
}

func arenaSlots(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return usageError("arena slots does not accept arguments")
	}
	creds, err := loadCredentials()
	if err != nil {
		return err
	}
	client := newAPIClient(creds)

	var slots any
	if err := client.get(ctx, "/active-slots/", &slots); err != nil {
		return err
	}
	return writeOK(map[string]any{
		"slots": slots,
	})
}

func arenaSubmit(ctx context.Context, args []string) error {
	opts, err := parseArenaSubmitArgs(args)
	if err != nil {
		return err
	}
	responseData, err := opts.loadResponseData()
	if err != nil {
		return err
	}
	if len(responseData) == 0 {
		return &cliError{
			Code:        "RESPONSE_DATA_EMPTY",
			Message:     "response_data must be a non-empty JSON object",
			Recoverable: true,
		}
	}

	creds, err := loadCredentials()
	if err != nil {
		return err
	}
	client := newAPIClient(creds)

	body := map[string]any{
		"slot_id":       opts.slotID,
		"response_data": responseData,
	}
	var created any
	if err := client.post(ctx, "/responses/", body, &created); err != nil {
		return err
	}
	return writeOK(map[string]any{
		"response": created,
	})
}

type arenaSubmitOptions struct {
	slotID           string
	responseDataFile string
	responseDataJSON string
}

func parseArenaSubmitArgs(args []string) (*arenaSubmitOptions, error) {
	opts := &arenaSubmitOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--slot-id":
			i++
			if i >= len(args) {
				return nil, usageError("--slot-id requires a value")
			}
			opts.slotID = args[i]
		case "--response-data":
			i++
			if i >= len(args) {
				return nil, usageError("--response-data requires a file path")
			}
			opts.responseDataFile = args[i]
		case "--response-data-json":
			i++
			if i >= len(args) {
				return nil, usageError("--response-data-json requires a JSON object")
			}
			opts.responseDataJSON = args[i]
		default:
			return nil, usageError(fmt.Sprintf("unknown arena submit option %q", args[i]))
		}
	}
	if opts.slotID == "" {
		return nil, usageError("--slot-id is required")
	}
	if opts.responseDataFile == "" && opts.responseDataJSON == "" {
		return nil, usageError("--response-data or --response-data-json is required")
	}
	if opts.responseDataFile != "" && opts.responseDataJSON != "" {
		return nil, usageError("--response-data and --response-data-json cannot be used together")
	}
	return opts, nil
}

func (o *arenaSubmitOptions) loadResponseData() (map[string]any, error) {
	var raw []byte
	var err error
	if o.responseDataFile != "" {
		raw, err = os.ReadFile(o.responseDataFile)
		if err != nil {
			return nil, wrapError("RESPONSE_DATA_READ_FAILED", "failed to read response data file", err)
		}
	} else {
		raw = []byte(o.responseDataJSON)
	}

	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, wrapError("RESPONSE_DATA_INVALID_JSON", "response_data must be a JSON object", err)
	}
	if data == nil {
		return nil, &cliError{
			Code:        "RESPONSE_DATA_INVALID_TYPE",
			Message:     "response_data must be a JSON object",
			Recoverable: true,
		}
	}
	return data, nil
}
