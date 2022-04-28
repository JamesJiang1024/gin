package main

import (
	"errors"
	"fmt"

	envy "github.com/codegangsta/envy/lib"
	gin "github.com/codegangsta/gin/lib"
	shellwords "github.com/mattn/go-shellwords"
	cli "github.com/urfave/cli"

	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/0xAX/notificator"
)

var (
	startTime     = time.Now()
	logger        = log.New(os.Stdout, "[gin] ", 0)
	immediate     = false
	buildError    error
	colorGreen    = string([]byte{27, 91, 57, 55, 59, 51, 50, 59, 49, 109})
	colorRed      = string([]byte{27, 91, 57, 55, 59, 51, 49, 59, 49, 109})
	colorReset    = string([]byte{27, 91, 48, 109})
	notifier      = notificator.New(notificator.Options{AppName: "Gin Build"})
	notifications = false
)

func main() {
	app := cli.NewApp()
	app.Name = "gin"
	app.Usage = "A live reload utility for Go web applications."
	app.Action = MainAction
	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    "laddr,l",
			Value:   "",
			EnvVars: []string{"GIN_LADDR"},
			Usage:   "listening address for the proxy server",
		},
		&cli.IntFlag{
			Name:    "port,p",
			Value:   3000,
			EnvVars: []string{"GIN_PORT"},
			Usage:   "port for the proxy server",
		},
		&cli.IntFlag{
			Name:    "appPort,a",
			Value:   3001,
			EnvVars: []string{"BIN_APP_PORT"},
			Usage:   "port for the Go web server",
		},
		&cli.StringFlag{
			Name:    "bin,b",
			Value:   "gin-bin",
			EnvVars: []string{"GIN_BIN"},
			Usage:   "name of generated binary file",
		},
		&cli.StringFlag{
			Name:    "path,t",
			Value:   ".",
			EnvVars: []string{"GIN_PATH"},
			Usage:   "Path to watch files from",
		},
		&cli.StringFlag{
			Name:    "build,d",
			Value:   "",
			EnvVars: []string{"GIN_BUILD"},
			Usage:   "Path to build files from (defaults to same value as --path)",
		},
		&cli.StringSliceFlag{
			Name:    "excludeDir,x",
			Value:   &cli.StringSlice{},
			EnvVars: []string{"GIN_EXCLUDE_DIR"},
			Usage:   "Relative directories to exclude",
		},
		&cli.BoolFlag{
			Name:    "immediate,i",
			EnvVars: []string{"GIN_IMMEDIATE"},
			Usage:   "run the server immediately after it's built",
		},
		&cli.BoolFlag{
			Name:    "all",
			EnvVars: []string{"GIN_ALL"},
			Usage:   "reloads whenever any file changes, as opposed to reloading only on .go file change",
		},
		&cli.BoolFlag{
			Name:    "godep,g",
			EnvVars: []string{"GIN_GODEP"},
			Usage:   "use godep when building",
		},
		&cli.StringFlag{
			Name:    "buildArgs",
			EnvVars: []string{"GIN_BUILD_ARGS"},
			Usage:   "Additional go build arguments",
		},
		&cli.StringFlag{
			Name:    "certFile",
			EnvVars: []string{"GIN_CERT_FILE"},
			Usage:   "TLS Certificate",
		},
		&cli.StringFlag{
			Name:    "keyFile",
			EnvVars: []string{"GIN_KEY_FILE"},
			Usage:   "TLS Certificate Key",
		},
		&cli.StringFlag{
			Name:    "logPrefix",
			EnvVars: []string{"GIN_LOG_PREFIX"},
			Usage:   "Log prefix",
			Value:   "gin",
		},
		&cli.BoolFlag{
			Name:    "notifications",
			EnvVars: []string{"GIN_NOTIFICATIONS"},
			Usage:   "Enables desktop notifications",
		},
	}
	app.Commands = []*cli.Command{
		{
			Name:            "run",
			Aliases:         []string{"r"},
			Usage:           "Run the gin proxy in the current working directory",
			Action:          MainAction,
			SkipFlagParsing: true,
		},
		{
			Name:    "env",
			Aliases: []string{"e"},
			Usage:   "Display environment variables set by the .env file",
			Action:  EnvAction,
		},
	}

	app.Run(os.Args)
}

func MainAction(c *cli.Context) error {
	laddr := c.Value("laddr").(string)
	port := c.Value("port").(int)
	all := c.Value("all").(bool)
	appPort := c.Value("appPort").(string)
	immediate = c.Value("immediate").(bool)
	keyFile := c.Value("keyFile").(string)
	certFile := c.Value("certFile").(string)
	logPrefix := c.Value("logPrefix").(string)
	notifications = c.Value("notifications").(bool)

	logger.SetPrefix(fmt.Sprintf("[%s] ", logPrefix))

	// Bootstrap the environment
	envy.Bootstrap()

	// Set the PORT env
	os.Setenv("PORT", appPort)

	wd, err := os.Getwd()
	if err != nil {
		logger.Fatal(err)
	}

	buildArgs, err := shellwords.Parse(c.Value("buildArgs").(string))
	if err != nil {
		logger.Fatal(err)
	}

	buildPath := c.Value("build").(string)
	if buildPath == "" {
		buildPath = c.Value("path").(string)
	}
	builder := gin.NewBuilder(buildPath, c.Value("bin").(string), c.Value("godep").(bool), wd, buildArgs)
	runner := gin.NewRunner(filepath.Join(wd, builder.Binary()), c.Args().Slice()...)
	runner.SetWriter(os.Stdout)
	proxy := gin.NewProxy(builder, runner)

	config := &gin.Config{
		Laddr:    laddr,
		Port:     port,
		ProxyTo:  "http://localhost:" + appPort,
		KeyFile:  keyFile,
		CertFile: certFile,
	}

	err = proxy.Run(config)
	if err != nil {
		logger.Fatal(err)
	}

	if laddr != "" {
		logger.Printf("Listening at %s:%d\n", laddr, port)
	} else {
		logger.Printf("Listening on port %d\n", port)
	}

	shutdown(runner)

	// build right now
	build(builder, runner, logger)

	// scan for changes
	scanChanges(c.Value("path").(string), c.Value("excludeDir").([]string), all, func(path string) {
		runner.Kill()
		build(builder, runner, logger)
	})
	return nil
}

func EnvAction(c *cli.Context) error {
	logPrefix := c.Value("logPrefix").(string)
	logger.SetPrefix(fmt.Sprintf("[%s] ", logPrefix))

	// Bootstrap the environment
	env, err := envy.Bootstrap()
	if err != nil {
		logger.Fatalln(err)
	}

	for k, v := range env {
		fmt.Printf("%s: %s\n", k, v)
	}
	return nil
}

func build(builder gin.Builder, runner gin.Runner, logger *log.Logger) {
	logger.Println("Building...")

	if notifications {
		notifier.Push("Build Started!", "Building "+builder.Binary()+"...", "", notificator.UR_NORMAL)
	}
	err := builder.Build()
	if err != nil {
		buildError = err
		logger.Printf("%sBuild failed%s\n", colorRed, colorReset)
		fmt.Println(builder.Errors())
		buildErrors := strings.Split(builder.Errors(), "\n")
		if notifications {
			if err := notifier.Push("Build FAILED!", buildErrors[1], "", notificator.UR_CRITICAL); err != nil {
				logger.Println("Notification send failed")
			}
		}
	} else {
		buildError = nil
		logger.Printf("%sBuild finished%s\n", colorGreen, colorReset)
		if immediate {
			runner.Run()
		}
		if notifications {
			if err := notifier.Push("Build Succeded", "Build Finished!", "", notificator.UR_CRITICAL); err != nil {
				logger.Println("Notification send failed")
			}
		}
	}

	time.Sleep(100 * time.Millisecond)
}

type scanCallback func(path string)

func scanChanges(watchPath string, excludeDirs []string, allFiles bool, cb scanCallback) {
	for {
		filepath.Walk(watchPath, func(path string, info os.FileInfo, err error) error {
			if path == ".git" && info.IsDir() {
				return filepath.SkipDir
			}
			for _, x := range excludeDirs {
				if x == path {
					return filepath.SkipDir
				}
			}

			// ignore hidden files
			if filepath.Base(path)[0] == '.' {
				return nil
			}

			if (allFiles || filepath.Ext(path) == ".go") && info.ModTime().After(startTime) {
				cb(path)
				startTime = time.Now()
				return errors.New("done")
			}

			return nil
		})
		time.Sleep(500 * time.Millisecond)
	}
}

func shutdown(runner gin.Runner) {
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		s := <-c
		log.Println("Got signal: ", s)
		err := runner.Kill()
		if err != nil {
			log.Print("Error killing: ", err)
		}
		os.Exit(1)
	}()
}
