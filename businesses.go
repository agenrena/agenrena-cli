package main

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func runBusinesses(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing businesses command")
	}
	switch args[0] {
	case "offerings":
		return runBusinessOfferings(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown businesses command %q", args[0]))
	}
}

func runBusinessOfferings(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError("missing businesses offerings command")
	}
	switch args[0] {
	case "search-options":
		return businessOfferingsSearchOptions(ctx, args[1:])
	case "search":
		return businessOfferingsSearch(ctx, args[1:])
	case "list":
		return businessOfferingsList(ctx, args[1:])
	default:
		return usageError(fmt.Sprintf("unknown businesses offerings command %q", args[0]))
	}
}

type businessOfferingSearchOptions struct {
	category       string
	stateCode      string
	cityID         *int
	partySize      *int
	priceMax       *int
	servicePeriods []string
	requiredTags   []string
	preferredTags  []string
	latitude       string
	longitude      string
	page           int
}

func businessOfferingsSearch(ctx context.Context, args []string) error {
	opts, err := parseBusinessOfferingSearchArgs(args)
	if err != nil {
		return err
	}

	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	body := opts.requestBody()

	endpoint := "/business-offerings/search/"
	if opts.page > 1 {
		endpoint += "?page=" + url.QueryEscape(strconv.Itoa(opts.page))
	}
	var result any
	if err := client.post(ctx, endpoint, body, &result); err != nil {
		return err
	}
	return writeOK(result)
}

func (o *businessOfferingSearchOptions) requestBody() map[string]any {
	body := map[string]any{"category": o.category}
	if o.stateCode != "" {
		body["state_code"] = o.stateCode
	}
	if o.cityID != nil {
		body["city_id"] = *o.cityID
	}
	if o.partySize != nil {
		body["party_size"] = *o.partySize
	}
	if o.priceMax != nil {
		body["price_max"] = *o.priceMax
	}
	if len(o.servicePeriods) > 0 {
		body["service_periods"] = o.servicePeriods
	}
	if len(o.requiredTags) > 0 {
		body["required_tags"] = o.requiredTags
	}
	if len(o.preferredTags) > 0 {
		body["preferred_tags"] = o.preferredTags
	}
	if o.latitude != "" {
		body["latitude"] = o.latitude
		body["longitude"] = o.longitude
	}
	return body
}

func parseBusinessOfferingSearchArgs(args []string) (*businessOfferingSearchOptions, error) {
	opts := &businessOfferingSearchOptions{page: 1}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--category":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			opts.category = strings.TrimSpace(value)
			i = next
		case "--state-code":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			opts.stateCode = strings.TrimSpace(value)
			i = next
		case "--city-id":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			parsed, err := parseIntOption("--city-id", value, 1)
			if err != nil {
				return nil, err
			}
			opts.cityID = &parsed
			i = next
		case "--party-size":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			parsed, err := parseIntOption("--party-size", value, 1)
			if err != nil {
				return nil, err
			}
			opts.partySize = &parsed
			i = next
		case "--price-max":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			parsed, err := parseIntOption("--price-max", value, 0)
			if err != nil {
				return nil, err
			}
			opts.priceMax = &parsed
			i = next
		case "--service-period":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			opts.servicePeriods = append(opts.servicePeriods, value)
			i = next
		case "--required-tag":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			opts.requiredTags = append(opts.requiredTags, value)
			i = next
		case "--preferred-tag":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			opts.preferredTags = append(opts.preferredTags, value)
			i = next
		case "--latitude":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			opts.latitude = strings.TrimSpace(value)
			i = next
		case "--longitude":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			opts.longitude = strings.TrimSpace(value)
			i = next
		case "--page":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			opts.page, err = parseIntOption("--page", value, 1)
			if err != nil {
				return nil, err
			}
			i = next
		default:
			return nil, usageError(fmt.Sprintf("unknown businesses offerings search option %q", args[i]))
		}
	}
	if opts.category == "" {
		return nil, usageError("--category is required")
	}
	if (opts.latitude == "") != (opts.longitude == "") {
		return nil, usageError("--latitude and --longitude must be used together")
	}
	return opts, nil
}

type businessOfferingSearchOptionsQuery struct {
	countryCode string
	stateCode   string
}

func businessOfferingsSearchOptions(ctx context.Context, args []string) error {
	opts, err := parseBusinessOfferingSearchOptionsArgs(args)
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	query := url.Values{"country_code": []string{opts.countryCode}}
	if opts.stateCode != "" {
		query.Set("state_code", opts.stateCode)
	}
	var result any
	if err := client.get(ctx, "/business-offerings/search-options/?"+query.Encode(), &result); err != nil {
		return err
	}
	return writeOK(result)
}

func parseBusinessOfferingSearchOptionsArgs(args []string) (*businessOfferingSearchOptionsQuery, error) {
	opts := &businessOfferingSearchOptionsQuery{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--country-code":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			opts.countryCode = strings.TrimSpace(value)
			i = next
		case "--state-code":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return nil, err
			}
			opts.stateCode = strings.TrimSpace(value)
			i = next
		default:
			return nil, usageError(fmt.Sprintf("unknown businesses offerings search-options option %q", args[i]))
		}
	}
	if opts.countryCode == "" {
		return nil, usageError("--country-code is required")
	}
	return opts, nil
}

func businessOfferingsList(ctx context.Context, args []string) error {
	identityID, err := parseBusinessOfferingListArgs(args)
	if err != nil {
		return err
	}
	client, err := authenticatedClient()
	if err != nil {
		return err
	}

	var result any
	if err := client.get(ctx, fmt.Sprintf("/businesses/%s/offerings/", identityID), &result); err != nil {
		return err
	}
	return writeOK(result)
}

func parseBusinessOfferingListArgs(args []string) (string, error) {
	identityID := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--identity-id":
			value, next, err := requiredOptionValue(args, i)
			if err != nil {
				return "", err
			}
			identityID = strings.TrimSpace(value)
			i = next
		default:
			return "", usageError(fmt.Sprintf("unknown businesses offerings list option %q", args[i]))
		}
	}
	if identityID == "" {
		return "", usageError("--identity-id is required")
	}
	return identityID, nil
}
