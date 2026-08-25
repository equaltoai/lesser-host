// Command hosted-genesis-microvm-prune is the deploy-time version-pruning
// step for the hosted-genesis AWS::Lambda::MicrovmImage (issue #1052).
//
// The service hard-caps an image at 50 versions and publishes a new version on
// every content-changed deploy, so an unpruned image eventually 402s the
// deploy. This tool runs from the AppTheory deploy wrapper
// (scripts/app-theory-cdk.sh) BEFORE cdk deploy, so the cap can never bite:
// it keeps the newest N versions (default 5) of the image owned by the
// deploy's stage and deletes the rest, serially with settle-waits.
//
// Usage (invoked by the deploy contract; not a manual step):
//
//	go run ./scripts/hosted-genesis-microvm-prune prune <lab|live>
//
// Keep-N can be overridden with LESSER_HOST_MICROVM_KEEP_N or -keep-n.
//
// Exit codes:
//   - 0: pruned (or nothing to prune); the image is under the 50-version cap.
//   - 1: fail-closed — the image could not be brought under the cap, headroom
//     could not be verified, or the tool was misused. The deploy must not run.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambdamicrovms"

	"github.com/equaltoai/lesser-host/internal/microvmversions"
)

const defaultKeepN = 5

func main() {
	out, err := run(os.Args[1:], newClient)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hosted-genesis-microvm-prune: %v\n", err)
		os.Exit(1)
	}
	if out != "" {
		if _, err := os.Stderr.WriteString(out + "\n"); err != nil {
			fmt.Fprintf(os.Stderr, "hosted-genesis-microvm-prune: write output: %v\n", err)
			os.Exit(1)
		}
	}
}

// newClient builds the lambdamicrovms client from the ambient AWS credentials.
// The deploy wrapper exports short-lived credentials into the environment, so
// the default config chain picks them up without any profile handling here.
func newClient() (microvmversions.Client, error) {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return microvmversions.NewSDKClient(lambdamicrovms.NewFromConfig(cfg)), nil
}

// run parses args and performs the prune. makeClient is injectable for tests.
func run(args []string, makeClient func() (microvmversions.Client, error)) (string, error) {
	keepN := defaultKeepN
	if env := os.Getenv("LESSER_HOST_MICROVM_KEEP_N"); env != "" {
		n, err := strconv.Atoi(env)
		if err != nil || n < 1 {
			return "", fmt.Errorf("invalid LESSER_HOST_MICROVM_KEEP_N %q: want a positive integer", env)
		}
		keepN = n
	}

	fs := flag.NewFlagSet("hosted-genesis-microvm-prune", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.IntVar(&keepN, "keep-n", keepN, "keep the newest N image versions")
	if len(args) == 0 || args[0] != "prune" {
		return "", errors.New("usage: hosted-genesis-microvm-prune prune [-keep-n N] <lab|live>")
	}
	// flag parsing stops at the first positional argument, so the stage must
	// come after any flags (the deploy wrapper calls `prune <stage>`).
	if err := fs.Parse(args[1:]); err != nil {
		return "", err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return "", errors.New("usage: hosted-genesis-microvm-prune prune [-keep-n N] <lab|live>")
	}
	stage := rest[0]
	if stage != "lab" && stage != "live" {
		return "", fmt.Errorf("invalid stage %q: want lab or live", stage)
	}

	client, err := makeClient()
	if err != nil {
		return "", err
	}
	pruner := microvmversions.NewPruner(client, microvmversions.WithKeepN(keepN))

	res, err := pruner.Prune(context.Background(), microvmversions.ImageNameForStage(stage))
	if err != nil {
		return "", err
	}
	return formatResult(res, keepN), nil
}

func formatResult(res microvmversions.Result, keepN int) string {
	var b []byte
	b = append(b, fmt.Sprintf("hosted-genesis microvm image pruning (keep newest %d, cap %d):", keepN, microvmversions.MaxVersionsPerImage)...)
	b = append(b, fmt.Sprintf("\n  image: %s", res.ImageName)...)
	if res.ImageARN != "" {
		b = append(b, fmt.Sprintf(" (%s)", res.ImageARN)...)
	}
	b = append(b, fmt.Sprintf("\n  listed versions: %d", res.Listed)...)
	b = append(b, fmt.Sprintf("\n  deleted: %d", res.Deleted)...)
	if res.DeleteFailed > 0 {
		b = append(b, fmt.Sprintf(" (failed: %d — deploy continues, image still has headroom)", res.DeleteFailed)...)
	}
	b = append(b, fmt.Sprintf("\n  remaining after prune: %d", res.RemainingCount)...)
	for _, w := range res.Warnings {
		b = append(b, fmt.Sprintf("\n  warning: %s", w)...)
	}
	return string(b)
}
