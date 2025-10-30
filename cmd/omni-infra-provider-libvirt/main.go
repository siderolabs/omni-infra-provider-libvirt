// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package main is the root cmd of the provider script.
package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/digitalocean/go-libvirt"
	"github.com/siderolabs/omni/client/pkg/client"
	"github.com/siderolabs/omni/client/pkg/infra"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"

	"github.com/siderolabs/omni-infra-provider-libvirt/internal/pkg/config"
	"github.com/siderolabs/omni-infra-provider-libvirt/internal/pkg/provider"
	"github.com/siderolabs/omni-infra-provider-libvirt/internal/pkg/provider/meta"
)

//go:embed data/schema.json
var schema string

//go:embed data/icon.svg
var icon []byte

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:          "provider",
	Short:        "LibVirt Omni infrastructure provider",
	Long:         `Connects to Omni as an infra provider and manages VMs in Libvirt`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		loggerConfig := zap.NewProductionConfig()

		logger, err := loggerConfig.Build(
			zap.AddStacktrace(zapcore.ErrorLevel),
		)
		if err != nil {
			return fmt.Errorf("failed to create logger: %w", err)
		}

		if cfg.omniAPIEndpoint == "" {
			return fmt.Errorf("omni-api-endpoint flag is not set")
		}

		var config config.Config

		configRaw, err := os.Open(cfg.configFile)
		if err != nil {
			return fmt.Errorf("failed to read libvirt config file %q", cfg.configFile)
		}

		decoder := yaml.NewDecoder(configRaw)

		if err = decoder.Decode(&config); err != nil {
			return fmt.Errorf("failed to read libvirt config file %q", cfg.configFile)
		}

		libvirtConfig := config.LibVirt

		//nolint:errcheck
		uri, _ := url.Parse(string(libvirt.QEMUSystem))
		if libvirtConfig.URI != "" {
			uri, err = url.Parse(libvirtConfig.URI)
			if err != nil {
				return fmt.Errorf("bad libvirt connection URI: %s", libvirtConfig.URI)
			}
		}

		logger.Info("libvirt URI", zap.String("URI", uri.String()))

		libvirtClient, err := libvirt.ConnectToURI(uri)
		if err != nil {
			return fmt.Errorf("error connecting to libvirt: %w", err)
		}

		if !libvirtClient.IsConnected() {
			return errors.New("client is not connected")
		}
		ver, err := libvirtClient.ConnectGetVersion()
		if err != nil {
			return fmt.Errorf("error fetching version: %w", err)
		} else {
			logger.Info(fmt.Sprintf("libvirtVersion: %d", ver))
		}

		provisioner := provider.NewProvisioner(libvirtClient)

		ip, err := infra.NewProvider(meta.ProviderID, provisioner, infra.ProviderConfig{
			Name:        cfg.providerName,
			Description: cfg.providerDescription,
			Icon:        base64.RawStdEncoding.EncodeToString(icon),
			Schema:      schema,
		})
		if err != nil {
			return fmt.Errorf("failed to create infra provider: %w", err)
		}

		logger.Info("starting infra provider")

		clientOptions := []client.Option{
			client.WithInsecureSkipTLSVerify(cfg.insecureSkipVerify),
		}

		if cfg.serviceAccountKey != "" {
			clientOptions = append(clientOptions, client.WithServiceAccount(cfg.serviceAccountKey))
		}

		return ip.Run(cmd.Context(), logger, infra.WithOmniEndpoint(cfg.omniAPIEndpoint), infra.WithClientOptions(
			clientOptions...,
		))
	},
}

var cfg struct {
	omniAPIEndpoint     string
	serviceAccountKey   string
	providerName        string
	providerDescription string
	configFile          string
	insecureSkipVerify  bool
}

func main() {
	if err := app(); err != nil {
		os.Exit(1)
	}
}

func app() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)
	defer cancel()

	return rootCmd.ExecuteContext(ctx)
}

func init() {
	rootCmd.Flags().StringVar(&cfg.omniAPIEndpoint, "omni-api-endpoint", os.Getenv("OMNI_ENDPOINT"),
		"the endpoint of the Omni API, if not set, defaults to OMNI_ENDPOINT env var.")
	rootCmd.Flags().StringVar(&meta.ProviderID, "id", meta.ProviderID, "the id of the infra provider, it is used to match the resources with the infra provider label.")
	rootCmd.Flags().StringVar(&cfg.serviceAccountKey, "omni-service-account-key", os.Getenv("OMNI_SERVICE_ACCOUNT_KEY"), "Omni service account key, if not set, defaults to OMNI_SERVICE_ACCOUNT_KEY.")
	rootCmd.Flags().StringVar(&cfg.providerName, "provider-name", "libvirt", "provider name as it appears in Omni")
	rootCmd.Flags().StringVar(&cfg.providerDescription, "provider-description", "libVirt infrastructure provider", "Provider description as it appears in Omni")
	rootCmd.Flags().BoolVar(&cfg.insecureSkipVerify, "insecure-skip-verify", false, "ignores untrusted certs on Omni side")
	rootCmd.Flags().StringVar(&cfg.configFile, "config-file", "", "libvirt provider config")
}
