package main

import (
	"fmt"

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
			Name:  "port,p",
			Value: 3000,
			Usage: "port for the proxy server",
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
	port := c.GlobalInt("port")
	appPort := strconv.Itoa(c.GlobalInt("appPort"))
	immediate = !c.GlobalBool("lazy")

	// Set the PORT env
	os.Setenv("PORT", appPort)

	wd, err := os.Getwd()
	if err != nil {
		logger.Fatal(err)
	}

	builder := sparkplug.NewBuilder(c.GlobalString("path"), c.GlobalString("bin"), c.GlobalBool("godep"))
	runner := sparkplug.NewRunner(filepath.Join(wd, builder.Binary()), c.Args()...)
	runner.SetWriter(os.Stdout)
	proxy := sparkplug.NewProxy(builder, runner)

	config := &sparkplug.Config{
		Port:     port,
		ProxyTo:  "http://localhost:" + appPort,
		Endpoint: c.GlobalString("endpoint"),
	}

	err = proxy.Run(config, func() {
		runner.Kill()
		build(builder, runner, logger)
	})

	if err != nil {
		logger.Fatal(err)
	}

	logger.Printf("listening on port %d\n", port)

	shutdown(runner)

	// build right now
	build(builder, runner, logger)

	return nil
}

func build(builder sparkplug.Builder, runner sparkplug.Runner, logger *log.Logger) {
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

func shutdown(runner sparkplug.Runner) {
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		s := <-c
		logger.Println("Got signal: ", s)
		err := runner.Kill()
		if err != nil {
			logger.Print("Error killing: ", err)
		}
		os.Exit(1)
	}()
}
