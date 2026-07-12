package main

// Ad-hoc live check: create a Modal sandbox with the default 30s idle
// watchdog, never touch it, and verify the tag-authenticated guard
// snapshots + terminates it, then that it auto-resumes from the snapshot.
import (
	"context"
	"fmt"
	"os"
	"time"

	"sidekick/env"
)

func main() {
	ctx := context.Background()
	name := fmt.Sprintf("side-idlecheck-%d", time.Now().Unix())
	out, err := env.CreateSandboxActivity(ctx, env.CreateSandboxInput{EnvType: env.EnvTypeModal, Name: name})
	if err != nil {
		fmt.Println("create failed:", err)
		os.Exit(1)
	}
	fmt.Printf("created %s at %s:%d; waiting for idle hibernation...\n", name, out.SSHHost, out.SSHPort)
	defer env.DeleteSandboxActivity(context.Background(), env.DeleteSandboxInput{EnvType: env.EnvTypeModal, SandboxName: name})

	deadline := time.Now().Add(6 * time.Minute)
	for time.Now().Before(deadline) {
		check, err := env.CheckSandboxActivity(ctx, env.CheckSandboxInput{EnvType: env.EnvTypeModal, SandboxName: name})
		if err != nil {
			fmt.Println("check failed:", err)
			os.Exit(1)
		}
		if !check.Alive {
			fmt.Println("sandbox hibernated (terminated by guard)")
			resumed, err := env.CreateSandboxActivity(ctx, env.CreateSandboxInput{EnvType: env.EnvTypeModal, Name: name})
			if err != nil {
				fmt.Println("resume failed:", err)
				os.Exit(1)
			}
			fmt.Printf("resumed from snapshot at %s:%d (reused=%v)\n", resumed.SSHHost, resumed.SSHPort, resumed.Reused)
			return
		}
		time.Sleep(15 * time.Second)
	}
	fmt.Println("TIMEOUT: sandbox never hibernated — tag auth likely failing (guard 401s)")
	os.Exit(1)
}