// Package smoke loads pages in a remote browser over CDP and reports, for
// each, the document's HTTP status, console errors and a screenshot.
package smoke

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// Page is what checking one path found.
type Page struct {
	Path          string
	URL           string
	FinalURL      string
	Status        int
	ConsoleErrors []string
	Screenshot    []byte
	Duration      time.Duration
	// Err is why the page failed, or nil.
	Err error
}

// Checker checks paths under BaseURL.
type Checker struct {
	BaseURL string
	// Headers are sent with every request, such as a Vercel protection bypass.
	Headers map[string]any
	// Timeout bounds each page.
	Timeout             time.Duration
	FailOnConsoleErrors bool
	Log                 io.Writer
}

// ErrProtected reports a page that landed on Vercel's login instead of the
// site, because Deployment Protection is on and no bypass secret was sent.
var ErrProtected = errors.New("redirected to Vercel's login: the deployment is behind Vercel Deployment Protection. " +
	"Set VERCEL_AUTOMATION_BYPASS_SECRET to the project's Protection Bypass for Automation secret")

// Run connects to the browser at wsURL and checks each path in turn, in one tab.
func (c Checker) Run(ctx context.Context, wsURL string, paths []string) ([]Page, error) {
	allocCtx, cancelAlloc := chromedp.NewRemoteAllocator(ctx, wsURL, chromedp.NoModifyURL)
	defer cancelAlloc()
	tabCtx, cancelTab := chromedp.NewContext(allocCtx)
	defer cancelTab()

	var (
		mu       sync.Mutex
		status   int
		finalURL string
		console  []string
	)
	chromedp.ListenTarget(tabCtx, func(ev any) {
		mu.Lock()
		defer mu.Unlock()
		switch ev := ev.(type) {
		case *network.EventResponseReceived:
			if ev.Type == network.ResourceTypeDocument && ev.Response != nil {
				status = int(ev.Response.Status)
				finalURL = ev.Response.URL
			}
		case *runtime.EventConsoleAPICalled:
			if ev.Type == runtime.APITypeError {
				console = append(console, consoleText(ev.Args))
			}
		case *runtime.EventExceptionThrown:
			if ev.ExceptionDetails != nil {
				console = append(console, exceptionText(ev.ExceptionDetails))
			}
		}
	})

	setup := []chromedp.Action{network.Enable(), runtime.Enable()}
	if len(c.Headers) > 0 {
		setup = append(setup, network.SetExtraHTTPHeaders(network.Headers(c.Headers)))
	}
	if err := chromedp.Run(tabCtx, setup...); err != nil {
		return nil, fmt.Errorf("connect to the Browserbase session: %w. "+
			"If the job goes through a proxy, it has to allow WebSocket connections to connect.browserbase.com", err)
	}

	pages := make([]Page, 0, len(paths))
	for _, path := range paths {
		mu.Lock()
		status, finalURL, console = 0, "", nil
		mu.Unlock()

		p := Page{Path: path, URL: resolve(c.BaseURL, path)}
		start := time.Now()
		pageCtx, cancel := context.WithTimeout(tabCtx, c.Timeout)
		err := chromedp.Run(pageCtx,
			chromedp.Navigate(p.URL),
			chromedp.WaitReady("body", chromedp.ByQuery),
			chromedp.FullScreenshot(&p.Screenshot, 80),
		)
		cancel()
		p.Duration = time.Since(start)

		mu.Lock()
		p.Status, p.FinalURL, p.ConsoleErrors = status, finalURL, console
		mu.Unlock()
		p.Err = c.judge(p, err)
		c.report(p)
		pages = append(pages, p)
		if ctx.Err() != nil {
			return pages, ctx.Err()
		}
	}
	return pages, nil
}

func (c Checker) judge(p Page, navErr error) error {
	switch {
	case vercelLogin(p.FinalURL):
		return ErrProtected
	case navErr != nil && errors.Is(navErr, context.DeadlineExceeded):
		return fmt.Errorf("did not load within %s", c.Timeout)
	case navErr != nil:
		return navErr
	case p.Status >= 400:
		return fmt.Errorf("HTTP %d", p.Status)
	case c.FailOnConsoleErrors && len(p.ConsoleErrors) > 0:
		return fmt.Errorf("%d console error(s): %s", len(p.ConsoleErrors), p.ConsoleErrors[0])
	}
	return nil
}

func (c Checker) report(p Page) {
	if c.Log == nil {
		return
	}
	result := "ok"
	if p.Err != nil {
		result = "FAIL: " + p.Err.Error()
	}
	fmt.Fprintf(c.Log, "setup-browserbase: %s -> %d in %s, %d console error(s): %s\n",
		p.Path, p.Status, p.Duration.Round(time.Millisecond), len(p.ConsoleErrors), result)
	for _, e := range p.ConsoleErrors {
		fmt.Fprintf(c.Log, "    console: %s\n", e)
	}
}

// resolve joins a path onto the base URL, keeping the base's own path.
func resolve(base, path string) string {
	if strings.Contains(path, "://") {
		return path
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

// vercelLogin reports whether u is Vercel's login or SSO page, where a
// protected deployment sends a browser with no bypass.
func vercelLogin(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return (host == "vercel.com" || host == "www.vercel.com") &&
		(strings.HasPrefix(parsed.Path, "/login") || strings.HasPrefix(parsed.Path, "/sso-api"))
}

func consoleText(args []*runtime.RemoteObject) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		switch {
		case len(a.Value) > 0:
			parts = append(parts, strings.Trim(string(a.Value), `"`))
		case a.Description != "":
			parts = append(parts, a.Description)
		}
	}
	return strings.Join(parts, " ")
}

func exceptionText(d *runtime.ExceptionDetails) string {
	if d.Exception != nil && d.Exception.Description != "" {
		return firstLine(d.Exception.Description)
	}
	return d.Text
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
