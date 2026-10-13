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
	}
	root.PersistentFlags().String("config", "", "TOML file (METADONALDS_CONFIG); default: ./config.toml, then XDG metadonalds/config.toml")
	// Each binding refers to a flag defined immediately above it.
	_ = settings.BindPFlag("config", root.PersistentFlags().Lookup("config"))
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
			return app.Serve(cmd.Context(), cfg)
		},
	}
	serve.Flags().String("listen", "", "override the HTTP listen address (METADONALDS_SERVER_LISTEN_ADDR)")
	_ = settings.BindPFlag("server.listen_addr", serve.Flags().Lookup("listen"))
	root.AddCommand(serve)
	addUI := func(name, description string) {
		options := viper.New()
		options.SetEnvPrefix("METADONALDS")
		options.AutomaticEnv()
		options.AllowEmptyEnv(true)
		cmd := &cobra.Command{
			Use: name, Short: description, Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				serverURL := options.GetString("server")
				stop, err := app.Ensure(cmd.Context(), serverURL, settings.GetString("config"))
				if err != nil {
					return err
				}
				defer stop()
				if name == "web" {
					return web.Run(cmd.Context(), serverURL, cmd.ErrOrStderr())
				}
				return tui.Run(cmd.Context(), serverURL)
			},
		}
		cmd.Flags().String("server", client.DefaultURL, "MetaDonalds HTTP(S) origin (METADONALDS_SERVER)")
		_ = options.BindPFlag("server", cmd.Flags().Lookup("server"))
		root.AddCommand(cmd)
	}
	addUI("tui", "Open the terminal interface (auto-start local server if needed)")
	addUI("web", "Open the web interface in your browser (auto-start local server if needed)")
	return root
}
