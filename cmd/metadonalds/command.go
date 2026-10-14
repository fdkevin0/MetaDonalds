package main

import (
	"fmt"
	"strings"

	"github.com/fdkevin0/MetaDonalds/internal/app"
	"github.com/fdkevin0/MetaDonalds/internal/client"
	"github.com/fdkevin0/MetaDonalds/internal/tui"
	"github.com/fdkevin0/MetaDonalds/internal/web"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newCommand() *cobra.Command {
	settings := viper.New()
	settings.SetEnvPrefix("METADONALDS")
	settings.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	settings.AutomaticEnv()
	settings.AllowEmptyEnv(true)
	root := &cobra.Command{
		Use: "metadonalds", Short: "Search data sources and submit resources to downloaders",
		SilenceUsage: true, SilenceErrors: true,
		Version: commandVersion(),
	}
	root.SetVersionTemplate("{{.Name}} {{.Version}}\n")
	root.PersistentFlags().String("config", "", "TOML file (METADONALDS_CONFIG); default: ./config.toml, then XDG metadonalds/config.toml")
	// Each binding refers to a flag defined immediately above it.
	_ = settings.BindPFlag("config", root.PersistentFlags().Lookup("config"))
	var openBrowser bool
	serve := &cobra.Command{
		Use: "serve", Short: "Run the HTTP API and WebUI", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			listen := settings.GetString("server.listen_addr")
			if cmd.Flags().Changed("listen") && strings.TrimSpace(listen) == "" {
				return fmt.Errorf("--listen must not be empty")
			}
			cfg, err := app.LoadConfig(settings.GetString("config"), listen)
			if err != nil {
				return err
			}
			var ready func(string)
			if openBrowser {
				ready = func(address string) { web.Open(address, cmd.ErrOrStderr()) }
			}
			return app.Serve(cmd.Context(), cfg, ready)
		},
	}
	serve.Flags().BoolVar(&openBrowser, "open", false, "open the WebUI in your default browser after startup")
	serve.Flags().String("listen", "", "override the HTTP listen address (METADONALDS_SERVER_LISTEN_ADDR)")
	_ = settings.BindPFlag("server.listen_addr", serve.Flags().Lookup("listen"))
	root.AddCommand(serve)
	options := viper.New()
	options.SetEnvPrefix("METADONALDS")
	options.AutomaticEnv()
	options.AllowEmptyEnv(true)
	terminal := &cobra.Command{
		Use: "tui", Short: "Open the terminal interface (auto-start local server if needed)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			serverURL := options.GetString("server")
			stop, err := app.Ensure(cmd.Context(), serverURL, settings.GetString("config"))
			if err != nil {
				return err
			}
			defer stop()
			return tui.Run(cmd.Context(), serverURL)
		},
	}
	terminal.Flags().String("server", client.DefaultURL, "MetaDonalds HTTP(S) origin (METADONALDS_SERVER)")
	_ = options.BindPFlag("server", terminal.Flags().Lookup("server"))
	root.AddCommand(terminal)
	return root
}
