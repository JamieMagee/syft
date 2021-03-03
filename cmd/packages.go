package cmd

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"github.com/anchore/syft/internal/presenter/packages"

	"github.com/anchore/syft/internal"
	"github.com/anchore/syft/internal/anchore"
	"github.com/anchore/syft/internal/bus"
	"github.com/anchore/syft/internal/log"
	"github.com/anchore/syft/internal/ui"
	"github.com/anchore/syft/syft"
	"github.com/anchore/syft/syft/distro"
	"github.com/anchore/syft/syft/event"
	"github.com/anchore/syft/syft/pkg"
	"github.com/anchore/syft/syft/source"
	"github.com/pkg/profile"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/wagoodman/go-partybus"
)

const sourceExample = `  Supports the following image sources:
    {{.appName}} {{.command}} yourrepo/yourimage:tag     defaults to using images from a Docker daemon
    {{.appName}} {{.command}} path/to/a/file/or/dir      a Docker tar, OCI tar, OCI directory, or generic filesystem directory 

  You can also explicitly specify the scheme to use:
    {{.appName}} {{.command}} docker:yourrepo/yourimage:tag          explicitly use the Docker daemon
    {{.appName}} {{.command}} docker-archive:path/to/yourimage.tar   use a tarball from disk for archives created from "docker save"
    {{.appName}} {{.command}} oci-archive:path/to/yourimage.tar      use a tarball from disk for OCI archives (from Skopeo or otherwise)
    {{.appName}} {{.command}} oci-dir:path/to/yourimage              read directly from a path on disk for OCI layout directories (from Skopeo or otherwise)
    {{.appName}} {{.command}} dir:path/to/yourproject                read directly from a path on disk (any directory)`

var packagesCmd = &cobra.Command{
	Use:   "packages [SOURCE]",
	Short: "Generate a package SBOM",
	Long:  "Generate a packaged-based Software Bill Of Materials (SBOM) from container images and filesystems",
	Example: internal.Tprintf(`  {{.appName}} packages alpine:latest                a summary of discovered packages
  {{.appName}} packages alpine:latest -o json        show all possible cataloging details
  {{.appName}} packages alpine:latest -o cyclonedx   show a CycloneDX SBOM
  {{.appName}} packages alpine:latest -vv            show verbose debug information

`+sourceExample, map[string]interface{}{
		"appName": internal.ApplicationName,
		"command": "packages",
	}),
	Args:          cobra.MaximumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			err := cmd.Help()
			if err != nil {
				return err
			}
			// silently exit
			return fmt.Errorf("")
		}

		if appConfig.Dev.ProfileCPU && appConfig.Dev.ProfileMem {
			return fmt.Errorf("cannot profile CPU and memory simultaneously")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if appConfig.Dev.ProfileCPU {
			defer profile.Start(profile.CPUProfile).Stop()
		} else if appConfig.Dev.ProfileMem {
			defer profile.Start(profile.MemProfile).Stop()
		}

		return packagesExec(cmd, args)
	},
	ValidArgsFunction: dockerImageValidArgsFunction,
}

func init() {
	setFormatOptions(packagesCmd.Flags())
	setUploadFlags(packagesCmd.Flags())
	setSourceOptions(packagesCmd.Flags())

	rootCmd.AddCommand(packagesCmd)
}

func setSourceOptions(flags *pflag.FlagSet) {
	flag := "scope"
	flags.StringP(
		"scope", "s", source.SquashedScope.String(),
		fmt.Sprintf("selection of layers to catalog, options=%v", source.AllScopes))
	if err := viper.BindPFlag(flag, flags.Lookup(flag)); err != nil {
		fmt.Printf("unable to bind flag '%s': %+v", flag, err)
		os.Exit(1)
	}
}

func setFormatOptions(flags *pflag.FlagSet) {
	// output & formatting options
	flag := "output"
	flags.StringP(
		flag, "o", string(packages.TablePresenterOption),
		fmt.Sprintf("report output formatter, options=%v", packages.AllPresenters),
	)
	if err := viper.BindPFlag(flag, flags.Lookup(flag)); err != nil {
		fmt.Printf("unable to bind flag '%s': %+v", flag, err)
		os.Exit(1)
	}
}

func setUploadFlags(flags *pflag.FlagSet) {
	flag := "host"
	flags.StringP(
		flag, "H", "",
		"the hostname or URL of the Anchore Enterprise instance to upload to",
	)
	if err := viper.BindPFlag("anchore.host", flags.Lookup(flag)); err != nil {
		fmt.Printf("unable to bind flag '%s': %+v", flag, err)
		os.Exit(1)
	}

	flag = "username"
	flags.StringP(
		flag, "u", "",
		"the username to authenticate against Anchore Enterprise",
	)
	if err := viper.BindPFlag("anchore.username", flags.Lookup(flag)); err != nil {
		fmt.Printf("unable to bind flag '%s': %+v", flag, err)
		os.Exit(1)
	}

	flag = "password"
	flags.StringP(
		flag, "p", "",
		"the password to authenticate against Anchore Enterprise",
	)
	if err := viper.BindPFlag("anchore.password", flags.Lookup(flag)); err != nil {
		fmt.Printf("unable to bind flag '%s': %+v", flag, err)
		os.Exit(1)
	}

	flag = "dockerfile"
	flags.StringP(
		flag, "d", "",
		"include dockerfile for upload to Anchore Enterprise",
	)
	if err := viper.BindPFlag("anchore.dockerfile", flags.Lookup(flag)); err != nil {
		fmt.Printf("unable to bind flag '#{flag}': #{err}")
		os.Exit(1)
	}

	flag = "overwrite-existing-image"
	flags.Bool(
		flag, false,
		"overwrite an existing image during the upload to Anchore Enterprise",
	)
	if err := viper.BindPFlag("anchore.overwrite-existing-image", flags.Lookup(flag)); err != nil {
		fmt.Printf("unable to bind flag '#{flag}': #{err}")
		os.Exit(1)
	}
}

func packagesExec(_ *cobra.Command, args []string) error {
	errs := packagesExecWorker(args[0])
	ux := ui.Select(appConfig.CliOptions.Verbosity > 0, appConfig.Quiet)
	return ux(errs, eventSubscription)
}

func packagesExecWorker(userInput string) <-chan error {
	errs := make(chan error)
	go func() {
		defer close(errs)

		checkForApplicationUpdate()

		src, cleanup, err := source.New(userInput)
		if err != nil {
			errs <- fmt.Errorf("failed to determine image source: %+v", err)
			return
		}
		defer cleanup()

		catalog, d, err := syft.CatalogPackages(src, appConfig.ScopeOpt)
		if err != nil {
			errs <- fmt.Errorf("failed to catalog input: %+v", err)
			return
		}

		if appConfig.Anchore.UploadEnabled {
			if err := runPackageSbomUpload(src, src.Metadata, catalog, d); err != nil {
				errs <- err
				return
			}
		}

		bus.Publish(partybus.Event{
			Type: event.PresenterReady,
			Value: packages.Presenter(appConfig.PresenterOpt, packages.PresenterConfig{
				SourceMetadata: src.Metadata,
				Catalog:        catalog,
				Distro:         d,
			}),
		})
	}()
	return errs
}

func runPackageSbomUpload(src source.Source, s source.Metadata, catalog *pkg.Catalog, d *distro.Distro) error {
	log.Infof("uploading results to %s", appConfig.Anchore.Host)

	if src.Metadata.Scheme != source.ImageScheme {
		return fmt.Errorf("unable to upload results: only images are supported")
	}

	var dockerfileContents []byte
	if appConfig.Anchore.Dockerfile != "" {
		if _, err := os.Stat(appConfig.Anchore.Dockerfile); os.IsNotExist(err) {
			return fmt.Errorf("unable to read dockerfile=%q: %w", appConfig.Anchore.Dockerfile, err)
		}

		fh, err := os.Open(appConfig.Anchore.Dockerfile)
		if err != nil {
			return fmt.Errorf("unable to open dockerfile=%q: %w", appConfig.Anchore.Dockerfile, err)
		}

		dockerfileContents, err = ioutil.ReadAll(fh)
		if err != nil {
			return fmt.Errorf("unable to read dockerfile=%q: %w", appConfig.Anchore.Dockerfile, err)
		}
	}

	var scheme string
	var hostname = appConfig.Anchore.Host
	urlFields := strings.Split(hostname, "://")
	if len(urlFields) > 1 {
		scheme = urlFields[0]
		hostname = urlFields[1]
	}

	c, err := anchore.NewClient(anchore.Configuration{
		Hostname: hostname,
		Username: appConfig.Anchore.Username,
		Password: appConfig.Anchore.Password,
		Scheme:   scheme,
	})
	if err != nil {
		return fmt.Errorf("failed to create anchore client: %+v", err)
	}

	importCfg := anchore.ImportConfig{
		ImageMetadata:           src.Image.Metadata,
		SourceMetadata:          s,
		Catalog:                 catalog,
		Distro:                  d,
		Dockerfile:              dockerfileContents,
		OverwriteExistingUpload: appConfig.Anchore.OverwriteExistingImage,
	}

	if err := c.Import(context.Background(), importCfg); err != nil {
		return fmt.Errorf("failed to upload results to host=%s: %+v", appConfig.Anchore.Host, err)
	}
	return nil
}