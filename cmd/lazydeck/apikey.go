package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/kevintcoughlin/lazydeck/internal/apikey"
)

// runAPIKey implements `lazydeck api-key create|list|revoke` (issue #50)
// against the key store at path.
func runAPIKey(args []string, path string, stdout, stderr io.Writer, now time.Time) error {
	if len(args) == 0 {
		return errors.New("usage: lazydeck api-key create|list|revoke (see lazydeck help)")
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("api-key create", flag.ContinueOnError)
		fs.SetOutput(stderr)
		name := fs.String("name", "", "a label for the key, e.g. ci-build (required)")
		scopes := fs.String("scopes", string(apikey.ScopeRead), "comma-separated scopes: read,discover,pair,deploy,logs:sync,jobs:cancel")
		expiresIn := fs.Duration("expires-in", 0, "lifetime, e.g. 2160h; 0 means no expiry")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() > 0 {
			return fmt.Errorf("unexpected argument %q", fs.Arg(0))
		}
		if *expiresIn < 0 {
			return errors.New("--expires-in must not be negative")
		}
		parsed, err := apikey.ParseScopes(*scopes)
		if err != nil {
			return err
		}
		token, key, err := apikey.Create(path, *name, parsed, *expiresIn, now)
		if err != nil {
			return err
		}
		// The key goes to stdout alone so `KEY=$(lazydeck api-key create
		// ...)` captures exactly it; everything else goes to stderr.
		_, _ = fmt.Fprintf(stderr, "Created API key %s (%s) with scopes %s", key.ID, key.Name, key.ScopeList())
		if key.ExpiresAt != nil {
			_, _ = fmt.Fprintf(stderr, ", expiring %s", key.ExpiresAt.Format(time.RFC3339))
		}
		_, _ = fmt.Fprintln(stderr, ".\nStore it in your CI secret store now: it is not saved anywhere and can't be shown again.")
		_, err = fmt.Fprintln(stdout, token)
		return err

	case "list":
		if len(args) > 1 {
			return fmt.Errorf("unexpected argument %q", args[1])
		}
		keys, err := apikey.Load(path)
		if err != nil {
			return err
		}
		if len(keys) == 0 {
			_, err := fmt.Fprintln(stdout, "No API keys. Create one with: lazydeck api-key create --name NAME --scopes read,deploy")
			return err
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "ID\tNAME\tSCOPES\tSTATE\tCREATED\tEXPIRES")
		for _, k := range keys {
			expires := "never"
			if k.ExpiresAt != nil {
				expires = k.ExpiresAt.Format(time.RFC3339)
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", k.ID, k.Name, k.ScopeList(), k.State(now), k.CreatedAt.Format(time.RFC3339), expires)
		}
		return tw.Flush()

	case "revoke":
		if len(args) != 2 {
			return errors.New("usage: lazydeck api-key revoke ID")
		}
		if err := apikey.Revoke(path, args[1], now); err != nil {
			return err
		}
		_, err := fmt.Fprintf(stdout, "Revoked API key %s. A running lazydeck serve rejects it from the next request.\n", args[1])
		return err
	}
	return fmt.Errorf("unknown api-key command %q (want create, list, or revoke)", args[0])
}
