// SPDX-FileCopyrightText: 2024 SUSE LLC
//
// SPDX-License-Identifier: Apache-2.0

package gpgadd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/uyuni-project/uyuni-tools/shared/testutils"
	"github.com/uyuni-project/uyuni-tools/shared/types"
)

func TestParamsParsing(t *testing.T) {
	args := []string{
		"--force",
		"--backend", "kubectl",
		"path/to/key",
	}

	// Test function asserting that the args are properly parsed
	tester := func(globalFlags *types.GlobalFlags, flags *gpgAddFlags,
		cmd *cobra.Command, args []string,
	) error {
		testutils.AssertTrue(t, "Error parsing --force", flags.Force)
		testutils.AssertEquals(t, "Error parsing --backend", "kubectl", flags.Backend)
		return nil
	}

	globalFlags := types.GlobalFlags{}
	cmd := newCmd(&globalFlags, tester)

	testutils.AssertHasAllFlags(t, cmd, args)

	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Errorf("command failed with error: %s", err)
	}
}