// SPDX-FileCopyrightText: 2023 SUSE LLC
//
// SPDX-License-Identifier: Apache-2.0

package podman

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/uyuni-project/uyuni-tools/mgradm/cmd/migrate/shared"
	"github.com/uyuni-project/uyuni-tools/mgradm/shared/podman"
	adm_utils "github.com/uyuni-project/uyuni-tools/mgradm/shared/utils"
	"github.com/uyuni-project/uyuni-tools/shared/types"
	"github.com/uyuni-project/uyuni-tools/shared/utils"
)

func migrateToPodman(globalFlags *types.GlobalFlags, flags *podmanMigrateFlags, cmd *cobra.Command, args []string) {
	// Find the SSH Socket and paths for the migration
	sshAuthSocket := shared.GetSshAuthSocket()
	sshConfigPath, sshKnownhostsPath := shared.GetSshPaths()

	scriptDir := shared.GenerateMigrationScript(args[0], false)
	defer os.RemoveAll(scriptDir)

	extraArgs := []string{
		"-e", "SSH_AUTH_SOCK",
		"-v", filepath.Dir(sshAuthSocket) + ":" + filepath.Dir(sshAuthSocket),
		"-v", scriptDir + ":/var/lib/uyuni-tools/",
	}

	if sshConfigPath != "" {
		extraArgs = append(extraArgs, "-v", sshConfigPath+":/tmp/ssh_config")
	}

	if sshKnownhostsPath != "" {
		extraArgs = append(extraArgs, "-v", sshKnownhostsPath+":/etc/ssh/ssh_known_hosts")
	}

	if utils.GetSELinuxMode() == "Enforcing" {
		customSELinuxPolicyPodmanLabel, customSELinuxPolicyPath := shared.GetCustomSELinuxPolicyDetails("uyuni")
		shared.InstallCustomSELinuxPolicy(customSELinuxPolicyPath)
		if customSELinuxPolicyPath != "" {
			log.Debug().Msgf("customSELinuxPolicyPodmanLabel: %s", customSELinuxPolicyPodmanLabel)
			extraArgs = append(extraArgs, "--security-opt", customSELinuxPolicyPodmanLabel)
		}
	}

	podman.PrepareImage(&flags.Image)

	log.Info().Msg("Migrating server")
	runContainer("uyuni-migration", flags.Image.Name, flags.Image.Tag, extraArgs,
		[]string{"/var/lib/uyuni-tools/migrate.sh"})

	// Read the extracted data
	tz, oldPgVersion, newPgVersion := shared.ReadContainerData(scriptDir)

	if oldPgVersion != newPgVersion {
		var migrationImage adm_utils.ImageFlags
		migrationImage.Name = flags.MigrationImage.Name
		if migrationImage.Name == "" {
			migrationImage.Name = fmt.Sprintf("%s-migration-%s-%s", flags.Image.Name, oldPgVersion, newPgVersion)
		}
		migrationImage.Tag = flags.MigrationImage.Tag
		log.Info().Msgf("Using migration image %s:%s", migrationImage.Name, migrationImage.Tag)
		podman.PrepareImage(&migrationImage)
		shared.GeneratePgMigrationScript(scriptDir, oldPgVersion, newPgVersion, false)
		runContainer("uyuni-pg-migration", migrationImage.Name, migrationImage.Tag, extraArgs,
			[]string{"/var/lib/uyuni-tools/migrate.sh"})
	}

	shared.GenerateFinalizePostgresMigrationScript(scriptDir, true, oldPgVersion != newPgVersion, true, true, false)
	runContainer("uyuni-migration", flags.Image.Name, flags.Image.Tag, extraArgs,
		[]string{"/var/lib/uyuni-tools/migrate.sh"})

	fullImage := fmt.Sprintf("%s:%s", flags.Image.Name, flags.Image.Tag)

	podman.GenerateSystemdService(tz, fullImage, false, viper.GetStringSlice("podman.arg"))

	// Start the service

	if err := utils.RunCmd("systemctl", "enable", "--now", "uyuni-server"); err != nil {
		log.Fatal().Err(err).Msgf("Failed to enable uyuni-server systemd service")
	}

	log.Info().Msg("Server migrated")

	podman.EnablePodmanSocket()
}

func runContainer(name string, image string, tag string, extraArgs []string, cmd []string) {

	podmanArgs := append([]string{"run", "--name", name}, podman.GetCommonParams()...)
	podmanArgs = append(podmanArgs, extraArgs...)

	for volumeName, containerPath := range utils.VOLUMES {
		podmanArgs = append(podmanArgs, "-v", volumeName+":"+containerPath)
	}

	podmanArgs = append(podmanArgs, image+":"+tag)
	podmanArgs = append(podmanArgs, cmd...)

	err := utils.RunCmdStdMapping("podman", podmanArgs...)

	if err != nil {
		log.Fatal().Err(err).Msgf("Failed to run %s container", name)
	}
}