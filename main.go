package main

import (
	"fmt"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/skeswa/sparkplug/lib"
	"gopkg.in/urfave/cli.v1"

	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

var (
	startTime  = time.Now()
	logger     = log.New(os.Stdout, "⚡  ", 0)
	immediate  = false
	buildError error
)

func main() {
	app := cli.NewApp()
	app.Name = "sparkplug"
	app.Usage = "Exposes an HTTP endpoint to restart your go server."
	app.Action = mainAction
	app.Flags = []cli.Flag{
		cli.IntFlag{
			Name:   "port,p",
			Value:  3000,
			Usage:  "port for the proxy server",
			EnvVar: "PORT",
		},
		cli.IntFlag{
			Name:  "appPort,a",
			Value: 3001,
			Usage: "port for the Go web server",
		},
		cli.StringFlag{
			Name:  "bin,b",
			Value: ".sparkplug-bin",
			Usage: "name of generated binary file",
		},
		cli.StringFlag{
			Name:  "endpoint,e",
			Value: "/restart",
			Usage: "endpoint that restarts the Go web server",
		},
		cli.StringFlag{
			Name:  "path,t",
			Value: ".",
			Usage: "path to the Go server source",
		},
		cli.BoolFlag{
			Name:  "lazy,l",
			Usage: "run the server on demand",
		},
		cli.BoolFlag{
			Name:  "godep,g",
			Usage: "use godep when building",
		},
	}
	app.Commands = []cli.Command{
		{
			Name:      "run",
			ShortName: "r",
			Usage:     "Run the gin proxy in the current working directory",
			Action:    mainAction,
		},
	}

	app.Run(os.Args)
}

func mainAction(c *cli.Context) error {
	// Localize some of the fla variables.
	port := c.GlobalInt("port")
	path := c.GlobalString("path")
	godep := c.GlobalBool("godep")
	binary := c.GlobalString("bin")
	appPort := strconv.Itoa(c.GlobalInt("appPort"))
	immediate = !c.GlobalBool("lazy")

	// Set the PORT env
	os.Setenv("PORT", appPort)

	// Exit if the working directory cannot be resolved.
	wd, err := os.Getwd()
	if err != nil {
		logger.Fatal(err)
	}

	// Initialize the core components of the rebuilding process: the builder and
	// the runner.
	builder := sparkplug.NewBuilder(path, binary, godep)
	runner := sparkplug.NewRunner(filepath.Join(wd, builder.Binary()), c.Args()...)
	runner.SetWriter(os.Stdout)
	proxy := sparkplug.NewProxy(builder, runner)

	config := &sparkplug.Config{
		Port:     port,
		ProxyTo:  "http://localhost:" + appPort,
		Endpoint: c.GlobalString("endpoint"),
	}

	// Callback that kicks off the re-building process.
	rebuilder := func() {
		// Notify the user of the restart.
		fmt.Println()
		logger.Println("Restarting web server...")
		fmt.Println()

		// First kill the running binary, then build the new one. It will start
		// after it exists on its own.
		runner.Kill()
		build(builder, runner)
	}

	// Start the proxy on the specified port.
	err = proxy.Run(config, rebuilder)
	if err != nil {
		logger.Fatalf("ERROR! Failed to start the proxy on port %d: %v.\n", port, err)
	}

	// Declare that the proxy has started.
	logger.Printf("Listening on port %d.\n\n", port)

	// Perform the initial build at the inception of the cli.
	build(builder, runner)
	// Start watching the filesystem after the initial build begins. Then keep
	// sparkplug running until it need not any longer.
	blockUntilExit(runner, createWatcher(path, binary, rebuilder))

	// This action will never error via cli.
	return nil
}

// Build kicks off the binary building process, and runs the bianry after the
// binary has been built.
func build(builder sparkplug.Builder, runner sparkplug.Runner) {
	err := builder.Build()
	if err != nil {
		buildError = err
		logger.Println("ERROR! Build failed.")
		fmt.Println(builder.Errors())
	} else {
		// print success only if there were errors before
		if buildError != nil {
			logger.Println("Build Successful")
		}
		buildError = nil
		if immediate {
			runner.Run()
		}
	}

	time.Sleep(100 * time.Millisecond)
}

// CreateWatcher kicks off the filesystem watcher on the specified path. The
// rebuilder parameter is invoked when there is a relevant filesystem change.
// This function returns the watcher to be closed later.
func createWatcher(path string, binary string, rebuilder func()) *fsnotify.Watcher {
	// First get an appropriate path for the watcher to use.
	absPath, err := filepath.Abs(path)
	if err != nil {
		logger.Fatalf("ERROR! Failed to resolve path: %v.\n", err)
	}

	// Use fsnotify to create a watcher. Should fail on unsupported platforms.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Fatalf("ERROR! Failed to watch filesystem: %v.\n", err)
	}

	// Add the source path to the watcher list. Exit if there is a problem.
	err = watcher.Add(absPath)
	if err != nil {
		logger.Fatalf("ERROR! Failed to watch filesystem: %v.\n", err)
	}

	// Watch indefinitely for watcher events.
	go func() {
		// In some situations, a temp file will be created during the build process.
		// That file follows this naming pattern, and must be ignored.
		umaskBinary := binary + "-go-tmp-umask"

		for event := range watcher.Events {
			// Ignore events on generated sparkplug binaries.
			if !strings.HasSuffix(event.Name, binary) &&
				!strings.HasSuffix(event.Name, umaskBinary) &&
				(event.Op&fsnotify.Write == fsnotify.Write ||
					event.Op&fsnotify.Rename == fsnotify.Rename ||
					event.Op&fsnotify.Create == fsnotify.Create ||
					event.Op&fsnotify.Remove == fsnotify.Remove) {
				// Restart the build process whenever a file is written, changed or
				// removed.
				rebuilder()
			}
		}
	}()

	// Watch indefinitely for watcher errors.
	go func() {
		for err := range watcher.Errors {
			logger.Fatalf("ERROR! Problem observed while watching filesystem: %v.\n", err)
		}
	}()

	return watcher
}

// BlockUntilExit blocks while it waits for a SIGTERM. When it hears one, it
// promptly kills the program.
func blockUntilExit(runner sparkplug.Runner, watcher *fsnotify.Watcher) {
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	// Block until the there is a SIGTERM.
	<-c

	// Kill the running binary,
	err := runner.Kill()
	if err != nil {
		logger.Fatalf("ERROR! Could not kill server: %v.\n", err)
	}

	// Kill the watcher.
	watcher.Close()

	// Exit amicably if terminated by SIGTERM.
	os.Exit(0)
}
