// Command setup-browserbase runs browser checks and Playwright tests on
// Browserbase from a CircleCI job.
//
// Supported platforms: linux, darwin, windows.
package main

import (
	"context"

	"github.com/alecthomas/kong"

	"github.com/circleci-functions/toolkit/functionyaml"
	"github.com/circleci-functions/toolkit/sdk"
)

type cli struct {
	Smoke      smokeCmd      `cmd:"" default:"withargs" help:"Load pages on Browserbase and check their status, console and look. Runs when no command is given."`
	Playwright playwrightCmd `cmd:"" help:"Run the project's Playwright tests on Browserbase sessions."`
}

func main() {
	sdk.Run(run)
}

func run(ctx context.Context, client *sdk.Client) error {
	var flags cli
	kCtx := kong.Parse(&flags,
		kong.Name("setup-browserbase"),
		kong.Description("Run browser checks and Playwright tests on Browserbase."),
		kong.UsageOnError(),
		functionyaml.Embed(),
	)
	kCtx.BindTo(ctx, (*context.Context)(nil))
	kCtx.Bind(client)
	return kCtx.Run()
}
