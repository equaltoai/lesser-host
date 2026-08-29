// Command hosted-genesis-microvm-prune is the deploy-time version-pruning
// step for the hosted-genesis AWS::Lambda::MicrovmImage (issue #1052).
//
// The service hard-caps an image at 50 versions and publishes a new version on
// every content-changed deploy, so an unpruned image eventually 402s the
// deploy. This tool runs from the AppTheory deploy wrapper
// (scripts/app-theory-cdk.sh) BEFORE cdk deploy and keeps the newest N live
// versions (default 5) of the image owned by the deploy's stage, deleting the
// rest serially with settle-waits. Fail-closed when the image cannot be
// brought under the cap — but the pruner is a risk reducer, not a hard
// guarantee: resolution and listing failures are surfaced loudly, never
// silently treated as a clean first deploy.
//
// Usage (invoked by the deploy contract; not a manual step):
//
//	go run ./scripts/hosted-genesis-microvm-prune prune <lab|live> [template-path]
//
// When template-path (the synthesized CloudFormation template) is given and it
// declares the AWS::Lambda::MicrovmImage resource, the image is REQUIRED: a
// resolution failure is fail-closed (image-name drift or out-of-band deletion
// of the whole image — not a first deploy). Only a template without the image
// resource permits the first-deploy no-op. The template-derived image name must
// also match the stage-derived name, so pruning can never target a drifted name.
//
// Keep-N can be overridden with LESSER_HOST_MICROVM_KEEP_N or -keep-n (both
// must be >= 1; the default comes from microvmversions.DefaultKeepN).
//
// Exit codes:
//   - 0: pruned (or nothing to prune); the image is under the 50-version cap.
//   - 1: fail-closed — the image could not be brought under the cap, headroom
//     could not be verified, the template-declared image could not be resolved,
//     or the tool was misused. The deploy must not run.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambdamicrovms"

	"github.com/equaltoai/lesser-host/internal/microvmversions"
)

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
	keepN := microvmversions.DefaultKeepN
	if env := os.Getenv("LESSER_HOST_MICROVM_KEEP_N"); env != "" {
		n, err := strconv.Atoi(env)
		if err != nil {
			return "", fmt.Errorf("invalid LESSER_HOST_MICROVM_KEEP_N %q: want an integer", env)
		}
		keepN = n
	}

	fs := flag.NewFlagSet("hosted-genesis-microvm-prune", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.IntVar(&keepN, "keep-n", keepN, "keep the newest N image versions")
	if len(args) == 0 || args[0] != "prune" {
		return "", errors.New("usage: hosted-genesis-microvm-prune prune [-keep-n N] <lab|live> [template-path]")
	}
	// flag parsing stops at the first positional argument, so the stage and
	// optional template path must come after any flags (the deploy wrapper
	// calls `prune <stage> <template-path>`).
	if err := fs.Parse(args[1:]); err != nil {
		return "", err
	}
	rest := fs.Args()
	if len(rest) < 1 || len(rest) > 2 {
		return "", errors.New("usage: hosted-genesis-microvm-prune prune [-keep-n N] <lab|live> [template-path]")
	}
	stage := rest[0]
	if stage != "lab" && stage != "live" {
		return "", fmt.Errorf("invalid stage %q: want lab or live", stage)
	}
	// Single validation for both override paths (env and -keep-n): keep-N must
	// be >= 1. Same rule as the pruner's documented contract, applied once
	// here so env and flag cannot disagree.
	if keepN < 1 {
		return "", fmt.Errorf("keep-n must be >= 1 (got %d)", keepN)
	}

	imageName := microvmversions.ImageNameForStage(stage)
	opts := []microvmversions.Option{microvmversions.WithKeepN(keepN)}
	if len(rest) == 2 {
		templateOpts, err := pruneOptionsFromTemplate(rest[1], imageName)
		if err != nil {
			return "", err
		}
		opts = append(opts, templateOpts...)
	}

	client, err := makeClient()
	if err != nil {
		return "", err
	}
	pruner := microvmversions.NewPruner(client, opts...)

	res, err := pruner.Prune(context.Background(), imageName)
	if err != nil {
		return "", err
	}
	return formatResult(res, keepN), nil
}

// pruneOptionsFromTemplate derives the pruner options from the optional
// synthesized template path: when the template declares the
// AWS::Lambda::MicrovmImage resource, the image becomes required (resolution
// failure is fail-closed) and the template-declared name must match the
// stage-derived target, so pruning can never target a drifted name.
func pruneOptionsFromTemplate(templatePath, imageName string) ([]microvmversions.Option, error) {
	derivedName, present, err := imageResourceNameFromTemplate(templatePath)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	if derivedName != imageName {
		return nil, fmt.Errorf(
			"synthesized template declares microvm image %q but the stage-derived prune target is %q; "+
				"refusing to prune a possibly wrong image", derivedName, imageName)
	}
	return []microvmversions.Option{microvmversions.WithImageRequired(true)}, nil
}

// imageResourceNameFromTemplate reports whether the synthesized CloudFormation
// template declares the AWS::Lambda::MicrovmImage resource and, when it does,
// returns its Name property. This is the discriminator between a genuine first
// deploy (no image resource in the template — resolution is expected to fail
// and exit 0 is right) and name drift / out-of-band deletion of an image the
// template still declares (fail-closed).
func imageResourceNameFromTemplate(path string) (name string, present bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false, fmt.Errorf("read synthesized template %s: %w", path, err)
	}
	var tmpl struct {
		Resources map[string]struct {
			Type       string `json:"Type"`
			Properties struct {
				Name string `json:"Name"`
			} `json:"Properties"`
		} `json:"Resources"`
	}
	if err := json.Unmarshal(raw, &tmpl); err != nil {
		return "", false, fmt.Errorf("parse synthesized template %s: %w", path, err)
	}
	var found []string
	for _, res := range tmpl.Resources {
		if res.Type == "AWS::Lambda::MicrovmImage" {
			found = append(found, res.Properties.Name)
		}
	}
	switch len(found) {
	case 0:
		return "", false, nil
	case 1:
		if found[0] == "" {
			return "", false, fmt.Errorf("synthesized template %s declares AWS::Lambda::MicrovmImage without a Name property", path)
		}
		return found[0], true, nil
	default:
		return "", false, fmt.Errorf("synthesized template %s declares %d AWS::Lambda::MicrovmImage resources, want exactly 1", path, len(found))
	}
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
