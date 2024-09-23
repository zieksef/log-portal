package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"git.uqpaytech.com/xufeng/log-portal/portal"
)

func main() {
	// mandatory
	fileURL := flag.String("u", "", "Log file URL.")
	// mandatory when enable file
	dir := flag.String("d", "", "Local log dir path, it is mandatory and takes effect only when the -enablefile is true.")

	// optional
	interval := flag.Int64("i", 2, "HTTP request interval in seconds.")
	tail := flag.Int64("t", 0, "Bytes read for the initial fetch.")
	lifetime := flag.Int64("l", 3, "Local log files lifetime in days.")
	enableFile := flag.Bool("enablefile", false, "Enable write log into file.")
	disableConsole := flag.Bool("diableconsole", false, "Disable output log on the console.")

	flag.Parse()

	if *fileURL == "" {
		fmt.Println("Please provide log file URL.")
		return
	}

	if *interval <= 0 {
		fmt.Println("Please provide non-negative integer for interval option.")
		return
	}

	if *tail < 0 {
		fmt.Println("Please provide positive integer for tail option.")
		return
	}

	if !*enableFile && *disableConsole {
		fmt.Println("Please provide either --enablefile or enable console output.")
		return
	}

	if *enableFile && (*dir == "") {
		fmt.Println("Please provide file dir path.")
		return
	}

	ptl := portal.New(*fileURL, *interval, *tail)

	if err := ptl.Init(); err != nil {
		fmt.Printf("Failed to initialize portal: %v.\n", err)
		return
	}

	if err := ptl.SetupWriter(*disableConsole, *enableFile, *dir, *lifetime); err != nil {
		fmt.Printf("Failed to setup writer: %v.\n", err)
		return
	}

	defer ptl.Finalize()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	go ptl.Start()

	sig := <-signals
	fmt.Printf("\n\n[Portal]: received signal[%v] and exiting...", sig)
}
