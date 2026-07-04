package main

import (
	"context"
	"fmt"
)

func runFurriBall(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing furriball command")
	}

	switch args[0] {
	case "pets":
		return furriBallPets(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown furriball command %q", args[0]))
	}
}

func furriBallPets(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return usageError("furriball pets does not accept arguments")
	}

	client, err := authenticatedClient()
	if err != nil {
		return err
	}
	return furriBallPetsWithClient(ctx, client)
}

func furriBallPetsWithClient(ctx context.Context, client *APIClient) error {
	var result any
	if err := client.get(ctx, "/furriball/pets/", &result); err != nil {
		return err
	}
	return writeOK(result)
}
