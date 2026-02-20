package tools

import (
	"fmt"
	"os"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

// googleClientOption creates a ClientOption for Google APIs using a Service Account
// with Domain-Wide Delegation. It impersonates the given email address.
func googleClientOption(saFile, email string, scopes ...string) (option.ClientOption, error) {
	data, err := os.ReadFile(saFile)
	if err != nil {
		return nil, fmt.Errorf("reading service account file: %w", err)
	}

	conf, err := google.JWTConfigFromJSON(data, scopes...)
	if err != nil {
		return nil, fmt.Errorf("parsing service account JSON: %w", err)
	}

	conf.Subject = email

	ts := conf.TokenSource(nil)
	return option.WithTokenSource(ts), nil
}
