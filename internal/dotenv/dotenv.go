// Copyright (c) 2026 Michael D Henderson. All rights reserved.

// Package dotenv wraps the joho/godotenv package to load environment files.
package dotenv

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"github.com/mdhender/ecv4/internal/cerrs"
)

const (
	ErrMissingEnvironment = cerrs.Error("missing environment")
	ErrUnknownEnvironment = cerrs.Error("unknown environment")
)

// Load uses the `joho/godotenv` package to load environment files in the working directory.
//
// Load the following files depending on ECV4_ENV, with the first file having the highest precedence,
// and .env having the lowest precedence:
//
// Priority  Filename______________   .gitignore?  Secrets?  Notes_______________________________
// Highest   .env.{{ECV4_ENV}}.local  Yes          Yes       Environment-specific local overrides
// 2nd       .env.local               Yes          Yes       Local overrides
// 3rd       .env.{{ECV4_ENV}}        No           Never     Shared environment-specific variables
// Lowest    .env                     No           Never     Shared for all environments
//
// WARNING: we are incompatible with bkeepers/dotenv since we load `.env.local` in test.
// Read https://github.com/bkeepers/dotenv/issues/418 for the history of this decision.
func Load(env string) error {
	if env == "" {
		return ErrMissingEnvironment
	}
	if env != "development" && env != "test" && env != "production" {
		return ErrUnknownEnvironment
	}

	for _, path := range []string{
		".env." + env + ".local", // highest priority: .env.{{ECV4_ENV}}.local
		".env.local",             // 2nd     priority: .env.local
		".env." + env,            // 3rd     priority: .env.{{ECV4_ENV}}
		".env",                   // lowest  priority: .env
	} {
		// verify path exists and is a regular file
		if sb, err := os.Stat(path); err != nil || !sb.Mode().IsRegular() {
			continue
		}
		err := godotenv.Load(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}

	return nil
}
