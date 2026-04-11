package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/dotaikit/browse/internal/browser"
	"github.com/dotaikit/browse/internal/cli"
	"github.com/dotaikit/browse/internal/server"
	"github.com/dotaikit/browse/internal/state"
)

var version = "dev"

const (
	defaultChromeURL          = "http://127.0.0.1:9222"
	defaultHeadedUserDataDir  = "~/.browse/chrome-profile"
	defaultHeadedWindowString = "1280x900"
)

type serveConfig struct {
	chromeURL         string
	chromeURLProvided bool
	port              int
	headed            bool
	userDataDir       string
	extensionPaths    []string
	windowSize        [2]int
	proxyServer       string
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "serve":
		runServer()
	case "version", "--version":
		fmt.Printf("browse %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		// CLI mode: forward command to server
		args := os.Args[2:]
		if err := cli.Run(command, args); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
			os.Exit(1)
		}
	}
}

func runServer() {
	cfg, err := parseServeConfig(os.Args[2:])
	if err != nil {
		log.Fatalf("invalid serve flags: %s", err)
	}

	token := state.NewToken()
	var srv *server.Server
	if cfg.headed {
		mgr, err := browser.NewHeaded(browser.HeadedOptions{
			UserDataDir:    cfg.userDataDir,
			ExtensionPaths: cfg.extensionPaths,
			WindowSize:     cfg.windowSize,
			ProxyServer:    cfg.proxyServer,
		})
		if err != nil {
			log.Fatalf("Failed to start: %s", err)
		}
		srv, err = server.NewWithManager(mgr, cfg.port, token)
		if err != nil {
			mgr.Close()
			log.Fatalf("Failed to start: %s", err)
		}
	} else {
		srv, err = server.New(cfg.chromeURL, cfg.port, token)
		if err != nil {
			log.Fatalf("Failed to start: %s", err)
		}
	}

	// Cleanup on exit
	defer srv.Shutdown()

	if err := srv.Start(); err != nil {
		log.Fatalf("Server error: %s", err)
	}
}

func parseServeConfig(args []string) (serveConfig, error) {
	windowSize, err := parseWindowSize(defaultHeadedWindowString)
	if err != nil {
		return serveConfig{}, err
	}

	cfg := serveConfig{
		chromeURL:   defaultChromeURL,
		port:        0,
		userDataDir: defaultHeadedUserDataDir,
		windowSize:  windowSize,
	}
	userDataDirProvided := false
	extensionProvided := false
	windowSizeProvided := false
	proxyProvided := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--chrome-url", "-c":
			if i+1 >= len(args) {
				return serveConfig{}, fmt.Errorf("missing value for %s", args[i])
			}
			i++
			cfg.chromeURL = args[i]
			cfg.chromeURLProvided = true

		case "--port", "-p":
			if i+1 >= len(args) {
				return serveConfig{}, fmt.Errorf("missing value for %s", args[i])
			}
			i++
			port, err := strconv.Atoi(args[i])
			if err != nil {
				return serveConfig{}, fmt.Errorf("invalid port %q", args[i])
			}
			cfg.port = port

		case "--headed":
			cfg.headed = true

		case "--user-data-dir":
			if i+1 >= len(args) {
				return serveConfig{}, fmt.Errorf("missing value for %s", args[i])
			}
			i++
			cfg.userDataDir = args[i]
			userDataDirProvided = true

		case "--extension":
			if i+1 >= len(args) {
				return serveConfig{}, fmt.Errorf("missing value for %s", args[i])
			}
			i++
			cfg.extensionPaths = parseExtensionPaths(args[i])
			extensionProvided = true

		case "--window-size":
			if i+1 >= len(args) {
				return serveConfig{}, fmt.Errorf("missing value for %s", args[i])
			}
			i++
			size, err := parseWindowSize(args[i])
			if err != nil {
				return serveConfig{}, err
			}
			cfg.windowSize = size
			windowSizeProvided = true

		case "--proxy":
			if i+1 >= len(args) {
				return serveConfig{}, fmt.Errorf("missing value for %s", args[i])
			}
			i++
			cfg.proxyServer = args[i]
			proxyProvided = true
		}
	}

	if cfg.headed && cfg.chromeURLProvided {
		return serveConfig{}, fmt.Errorf("--headed cannot be used with --chrome-url")
	}
	if !cfg.headed {
		headedOnlyFlags := make([]string, 0, 4)
		if userDataDirProvided {
			headedOnlyFlags = append(headedOnlyFlags, "--user-data-dir")
		}
		if extensionProvided {
			headedOnlyFlags = append(headedOnlyFlags, "--extension")
		}
		if windowSizeProvided {
			headedOnlyFlags = append(headedOnlyFlags, "--window-size")
		}
		if proxyProvided {
			headedOnlyFlags = append(headedOnlyFlags, "--proxy")
		}
		if len(headedOnlyFlags) > 0 {
			return serveConfig{}, fmt.Errorf("%s require --headed", strings.Join(headedOnlyFlags, ", "))
		}
	}

	return cfg, nil
}

func parseExtensionPaths(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	extensions := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		extensions = append(extensions, part)
	}
	return extensions
}

func parseWindowSize(raw string) ([2]int, error) {
	var size [2]int
	parts := strings.Split(strings.ToLower(strings.TrimSpace(raw)), "x")
	if len(parts) != 2 {
		return size, fmt.Errorf("invalid window size %q (expected WxH)", raw)
	}

	width, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || width <= 0 {
		return size, fmt.Errorf("invalid window width in %q", raw)
	}
	height, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || height <= 0 {
		return size, fmt.Errorf("invalid window height in %q", raw)
	}

	size[0] = width
	size[1] = height
	return size, nil
}

func printUsage() {
	fmt.Print(`browse — lightweight browser automation CLI (Go + CDP)

Usage:
  browse serve [--chrome-url URL | --headed] [--port N] [--user-data-dir DIR] [--extension PATHS] [--window-size WxH] [--proxy URL]
                                                Start the persistent server
  browse <command> [args...]                    Send command to server
  browse version                                Show CLI version

Server flags:
  --chrome-url, -c    Chrome CDP URL (default: http://127.0.0.1:9222, headless remote mode)
  --headed            Launch a local headed Chrome instance (mutually exclusive with --chrome-url)
  --user-data-dir     Headed mode profile dir (default: ~/.browse/chrome-profile)
  --extension         Comma-separated Chrome extension paths (headed mode only)
  --window-size       Headed mode window size (default: 1280x900)
  --proxy             Proxy server URL, e.g. socks5://127.0.0.1:1080 (headed only)
  --port, -p          Server port (default: random)

Commands:
  Navigation:   goto <url>, back, forward, reload, frame, url
  Snapshot:     snapshot [-i] [-d N] [-c] [-s CSS] [-D] [-a] [-o path] [-C]
  Interaction:  click, fill, type, hover, select, scroll, press, upload
                wait <ms|selector|--networkidle [ms]|--load [ms]|--domcontentloaded [ms]>
  Read:         text, html, links, js <expr>, forms, css, attrs
                is <visible|hidden|enabled|disabled|checked|focused|editable> <@ref|selector>
                cookies, storage [set <key> <value>], perf, eval <js-file>
  Write:        viewport <WxH>, useragent <string>, cookie <name>=<value>, cookie-import <json-file>
                cookie-import-browser <browser> --domain <domain> [--profile <name>]
                header <name>:<value>, dialog-accept [text], dialog-dismiss
  Monitor:      console [--clear|--errors], network [--clear|--errors], dialog [--clear|--errors]
  Visual:       screenshot [--viewport] [--clip x,y,w,h] [path]
  Tabs:         tabs, tab <index>, newtab [url], closetab <target-id|index>
  Meta:         status, chain, diff, pdf, responsive, handoff [msg], resume
                state <save|load> <name>, restart, stop

Examples:
  # Start Chrome with CDP enabled
  google-chrome --remote-debugging-port=9222

  # Start browse server (remote Chrome)
  browse serve

  # Start browse server in headed mode with persistent profile
  browse serve --headed --user-data-dir ~/.browse/chrome-profile

  # Use from any terminal (or via AI agent)
  browse goto https://example.com
  browse snapshot -i
  browse click @e3
  browse fill @e4 "hello world"
  browse wait --networkidle
  browse frame --name checkout-iframe
  browse frame main
  browse cookie-import-browser chrome --domain .github.com
  browse handoff "please check the payment page"
  browse resume
  browse storage set mykey myvalue
  browse is editable @e4
  browse screenshot --viewport /tmp/page.png
  browse screenshot --clip 0,0,500,300 /tmp/clip.png
  browse screenshot /tmp/page.png
`)
}
