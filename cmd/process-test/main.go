// Command process-test verifies internal/process.Manager against a REAL
// subprocess, launched through Manager.Start's actual, unmodified
// argument construction (`binary --model X --port N`) — not llama.cpp's
// real rpc-server (not guaranteed present in a dev environment, needs a
// real model file), but a real, killable, waitable OS process
// nonetheless, since Manager's own logic doesn't know or care what
// binary it's managing.
//
// This same compiled binary acts as its own stand-in "rpc-server": the
// --model VALUE (not a separate marker flag — Manager's argument shape
// is fixed and leaves no room to smuggle one in) is used as the signal.
// If --model's value is one of the sentinel strings below, this process
// behaves as a helper instead of running the test suite. This means
// every test exercises Manager's real, unmodified command construction,
// not a workaround shape.
//
// Delete once the real Agent command server exists and exercises
// process.Manager against real rpc-server.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"tether/internal/process"
)

const (
	// sleepHelperModel, when passed as --model, makes this process sleep
	// for --port's VALUE in seconds, then exit 0. Repurposing --port as
	// a duration is a deliberate, harmless abuse of Manager's fixed
	// argument shape for testing purposes only — Manager itself has no
	// idea, and doesn't need to, since it only cares about process
	// lifecycle, not what the flags mean to whatever binary it launches.
	sleepHelperModel = "__process_test_sleep_helper__"

	// crashHelperModel makes this process exit immediately with a
	// distinct non-zero status — standing in for rpc-server crashing on
	// startup (bad model file, port already in use, etc.).
	crashHelperModel = "__process_test_crash_helper__"

	crashExitCode = 17
)

// helperModel and helperPortArg extract the --model and --port values
// Manager.Start actually passed, without depending on their exact
// position in os.Args beyond "somewhere after the binary name" — kept
// slightly more defensive than a fixed-index lookup so this doesn't
// silently break if Manager's flag ORDER ever changes, only if the flag
// NAMES change (which would be a deliberate, visible edit to Manager
// itself).
func helperModel() string {
	for i := 1; i < len(os.Args)-1; i++ {
		if os.Args[i] == "--model" {
			return os.Args[i+1]
		}
	}
	return ""
}

func helperPortArg() string {
	for i := 1; i < len(os.Args)-1; i++ {
		if os.Args[i] == "--port" {
			return os.Args[i+1]
		}
	}
	return ""
}

func main() {
	switch helperModel() {
	case sleepHelperModel:
		var seconds int
		fmt.Sscanf(helperPortArg(), "%d", &seconds)
		time.Sleep(time.Duration(seconds) * time.Second)
		os.Exit(0)
	case crashHelperModel:
		os.Exit(crashExitCode)
	}
	runTests()
}

func selfPath() (string, error) {
	return os.Executable()
}

func runTests() {
	self, err := selfPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: could not find own executable path: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== Test 1: fresh Manager reports Stopped ===")
	func() {
		mgr := process.NewManager()
		if mgr.Status() != process.StatusStopped {
			fmt.Printf("  FAIL: expected StatusStopped, got %s\n", mgr.Status())
		} else {
			fmt.Println("  PASS")
		}
	}()

	fmt.Println()
	fmt.Println("=== Test 2: Stop() on a never-started Manager is a no-op, not an error ===")
	func() {
		mgr := process.NewManager()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := mgr.Stop(ctx); err != nil {
			fmt.Printf("  FAIL: Stop() on idle manager returned an error: %v\n", err)
		} else {
			fmt.Println("  PASS: Stop() returned nil")
		}
	}()

	fmt.Println()
	fmt.Println("=== Test 3: Start() -> Status()==Running -> Stop() -> Status()==Stopped ===")
	func() {
		mgr := process.NewManager()
		err := mgr.Start(process.StartParams{
			BinaryPath: self,
			ModelPath:  sleepHelperModel,
			Port:       30, // sleep 30s — long enough that we control when it ends, via Stop()
		})
		if err != nil {
			fmt.Printf("  FAIL: Start() returned an error: %v\n", err)
			return
		}
		time.Sleep(300 * time.Millisecond) // let the helper actually start running
		if mgr.Status() != process.StatusRunning {
			fmt.Printf("  FAIL: expected StatusRunning after Start(), got %s\n", mgr.Status())
		} else {
			fmt.Println("  PASS: Status() reports Running after Start()")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := mgr.Stop(ctx); err != nil {
			fmt.Printf("  FAIL: Stop() returned an error: %v\n", err)
		} else {
			fmt.Println("  PASS: Stop() returned nil")
		}
		if mgr.Status() != process.StatusStopped {
			fmt.Printf("  FAIL: expected StatusStopped after Stop(), got %s\n", mgr.Status())
		} else {
			fmt.Println("  PASS: Status() reports Stopped after Stop()")
		}
	}()

	fmt.Println()
	fmt.Println("=== Test 4: double-Start fails while one is already running ===")
	func() {
		mgr := process.NewManager()
		if err := mgr.Start(process.StartParams{BinaryPath: self, ModelPath: sleepHelperModel, Port: 10}); err != nil {
			fmt.Printf("  FAIL: first Start() failed unexpectedly: %v\n", err)
			return
		}
		time.Sleep(300 * time.Millisecond)

		err := mgr.Start(process.StartParams{BinaryPath: self, ModelPath: sleepHelperModel, Port: 10})
		if err == nil {
			fmt.Println("  FAIL: second Start() succeeded while one was already running")
		} else {
			fmt.Printf("  PASS: second Start() correctly rejected: %v\n", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		mgr.Stop(ctx) // cleanup, not itself asserted
	}()

	fmt.Println()
	fmt.Println("=== Test 5: unexpected exit is detected as StatusCrashed, with LastExitError set ===")
	func() {
		mgr := process.NewManager()
		if err := mgr.Start(process.StartParams{BinaryPath: self, ModelPath: crashHelperModel, Port: 0}); err != nil {
			fmt.Printf("  FAIL: Start() failed unexpectedly: %v\n", err)
			return
		}

		// The helper exits almost immediately (no sleep) — give
		// watchForExit's background goroutine a moment to actually
		// observe and record that exit before we check Status().
		time.Sleep(500 * time.Millisecond)

		if mgr.Status() != process.StatusCrashed {
			fmt.Printf("  FAIL: expected StatusCrashed after an unexpected exit, got %s\n", mgr.Status())
		} else {
			fmt.Println("  PASS: Status() correctly reports Crashed")
		}

		if err := mgr.LastExitError(); err == nil {
			fmt.Println("  FAIL: LastExitError() is nil after a crash")
		} else {
			fmt.Printf("  PASS: LastExitError() reports: %v\n", err)
		}
	}()

	fmt.Println()
	fmt.Println("=== Test 6: after a crash, Start() can be called again (not permanently stuck) ===")
	func() {
		mgr := process.NewManager()
		mgr.Start(process.StartParams{BinaryPath: self, ModelPath: crashHelperModel, Port: 0})
		time.Sleep(500 * time.Millisecond) // let it crash and be observed

		err := mgr.Start(process.StartParams{BinaryPath: self, ModelPath: sleepHelperModel, Port: 5})
		if err != nil {
			fmt.Printf("  FAIL: Start() after a crash was rejected: %v\n", err)
		} else {
			fmt.Println("  PASS: Start() after a crash succeeded")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		mgr.Stop(ctx)
	}()
}