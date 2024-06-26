// SPDX-FileCopyrightText: 2024 SUSE LLC
//
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"github.com/spf13/cobra"
	"github.com/uyuni-project/uyuni-tools/shared/api"
	. "github.com/uyuni-project/uyuni-tools/shared/l10n"
	"github.com/uyuni-project/uyuni-tools/shared/types"
	"github.com/uyuni-project/uyuni-tools/shared/utils"
)

type apiFlags struct {
	api.ConnectionDetails `mapstructure:"api"`
}

// NewCommand generates a JSON over HTTP API helper tool command.
func NewCommand(globalFlags *types.GlobalFlags) (*cobra.Command, error) {
	var flags apiFlags

	apiCmd := &cobra.Command{
		Use:   "api",
		Short: L("JSON over HTTP API helper tool"),
	}

	apiGet := &cobra.Command{
		Use:   "get path [parameters]...",
		Short: L("Call API GET request"),
		Long:  L("Takes an API path and optional parameters and then issues GET request with them. If user and password are provided, calls login before API call"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.CommandHelper(globalFlags, cmd, args, &flags, runGet)
		},
	}

	apiPost := &cobra.Command{
		Use:   "post path parameters...",
		Short: L("Call API POST request"),
		Long:  L("Takes an API path and parameters and then issues POST request with them. User and password are mandatory. Parameters can be either JSON encoded string or one or more key=value pairs."),
		RunE: func(cmd *cobra.Command, args []string) error {
			return utils.CommandHelper(globalFlags, cmd, args, &flags, runPost)
		},
	}

	apiCmd.AddCommand(apiGet)
	apiCmd.AddCommand(apiPost)

	if err := api.AddAPIFlags(apiCmd, false); err != nil {
		return apiCmd, err
	}
	return apiCmd, nil
}
