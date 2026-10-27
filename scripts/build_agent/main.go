package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"sidekick/common"
)

func main() {
	targetOS := flag.String("os", runtime.GOOS, "target operating system")
	targetArch := flag.String("arch", runtime.GOARCH, "target architecture")
	identity := flag.Bool("identity", false, "print the agent identity without building")
	flag.Parse()

	var result string
	var err error
	if *identity {
		result, err = common.GetAgentRemoteIdentity()
	} else {
		result, err = common.GetAgentBinaryPath(*targetOS, *targetArch)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(result)
}
