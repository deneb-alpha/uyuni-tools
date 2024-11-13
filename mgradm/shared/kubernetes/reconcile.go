// SPDX-FileCopyrightText: 2024 SUSE LLC
//
// SPDX-License-Identifier: Apache-2.0

//go:build !nok8s

package kubernetes

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/rs/zerolog/log"
	adm_utils "github.com/uyuni-project/uyuni-tools/mgradm/shared/utils"
	"github.com/uyuni-project/uyuni-tools/shared"
	"github.com/uyuni-project/uyuni-tools/shared/kubernetes"
	. "github.com/uyuni-project/uyuni-tools/shared/l10n"
	"github.com/uyuni-project/uyuni-tools/shared/ssl"
	"github.com/uyuni-project/uyuni-tools/shared/types"
	"github.com/uyuni-project/uyuni-tools/shared/utils"
)

// Reconcile upgrades, migrate or install the server.
func Reconcile(flags *KubernetesServerFlags, fqdn string) error {
	if _, err := exec.LookPath("kubectl"); err != nil {
		return errors.New(L("install kubectl before running this command"))
	}

	namespace := flags.Kubernetes.Uyuni.Namespace
	// Create the namespace if not present
	if err := CreateNamespace(namespace); err != nil {
		return err
	}

	serverImage, err := utils.ComputeImage(flags.Image.Registry, utils.DefaultTag, flags.Image)
	if err != nil {
		return utils.Errorf(err, L("failed to compute image URL"))
	}

	cnx := shared.NewConnection("kubectl", "", kubernetes.ServerFilter)

	// Create a secret using SCC credentials if any are provided
	pullSecret, err := kubernetes.GetSCCSecret(
		flags.Kubernetes.Uyuni.Namespace, &flags.Installation.SCC, kubernetes.ServerApp,
	)
	if err != nil {
		return err
	}

	// Do we have an existing deployment to upgrade?
	// This can be freshly synchronized data from a migration or a running instance to upgrade.
	hasDeployment := kubernetes.HasDeployment(namespace, kubernetes.ServerFilter)
	hasDatabase := kubernetes.HasVolume(namespace, "var-pgsql")
	isMigration := hasDatabase && !hasDeployment

	cocoReplicas := kubernetes.GetReplicas(namespace, CocoDeployName)
	if cocoReplicas != 0 && !flags.Coco.IsChanged {
		// Upgrade: detect the number of running coco replicas
		flags.Coco.Replicas = cocoReplicas
	}

	var inspectedData utils.ServerInspectData
	if hasDatabase {
		// Inspect the image and the existing volumes
		data, err := kubernetes.InspectServer(namespace, serverImage, flags.Image.PullPolicy, pullSecret)
		if err != nil {
			return err
		}
		inspectedData = *data

		// Use the inspected DB port and name if not defined in the flags
		if flags.Installation.DB.Port == 0 && data.DBPort != 0 {
			flags.Installation.DB.Port = data.DBPort
		}

		if flags.Installation.DB.Name == "" && data.DBName != "" {
			flags.Installation.DB.Name = data.DBName
		}

		// Do we have a running server deploy? which version is it?
		// If there is no deployment / image, don't check the uyuni / SUMA upgrades
		var runningData *utils.ServerInspectData
		if runningImage := getRunningServerImage(namespace); runningImage != "" {
			runningData, err = kubernetes.InspectServer(namespace, runningImage, "Never", pullSecret)
			if err != nil {
				return err
			}
		}

		// Run sanity checks for upgrade
		if err := adm_utils.SanityCheck(runningData, &inspectedData, serverImage); err != nil {
			return err
		}

		// Get the fqdn from the inspected data if possible. Ignore difference with input value for now.
		fqdn = inspectedData.Fqdn

		if hasDeployment {
			// Scale down all deployments relying on the DB since it will be brought down during upgrade.
			if cocoReplicas > 0 {
				if err := kubernetes.ReplicasTo(namespace, CocoDeployName, 0); err != nil {
					return utils.Errorf(err, L("cannot set confidential computing containers replicas to 0"))
				}
			}

			// Scale down server deployment if present to upgrade the DB
			if err := kubernetes.ReplicasTo(namespace, ServerDeployName, 0); err != nil {
				return utils.Errorf(err, L("cannot set server replicas to 0"))
			}
		}
	}

	// Don't check the FQDN too early or we may not have it in case of upgrade.
	if err := utils.IsValidFQDN(fqdn); err != nil {
		return err
	}

	mounts := GetServerMounts()
	mounts = TuneMounts(mounts, &flags.Volumes)

	if err := kubernetes.CreatePersistentVolumeClaims(namespace, mounts); err != nil {
		return err
	}

	if hasDatabase {
		oldPgVersion := inspectedData.CurrentPgVersion
		newPgVersion := inspectedData.ImagePgVersion

		// Run the DB Upgrade job if needed
		if oldPgVersion < newPgVersion {
			jobName, err := StartDBUpgradeJob(
				namespace, flags.Image.Registry, flags.Image, flags.DBUpgradeImage, pullSecret,
				oldPgVersion, newPgVersion,
			)
			if err != nil {
				return err
			}

			// Wait for ever for the job to finish: the duration of this job depends on the amount of data to upgrade
			if err := kubernetes.WaitForJob(namespace, jobName, -1); err != nil {
				return err
			}
		} else if oldPgVersion > newPgVersion {
			return fmt.Errorf(
				L("downgrading database from PostgreSQL %[1]d to %[2]d is not supported"), oldPgVersion, newPgVersion)
		}

		// Run DB finalization job
		schemaUpdateRequired := oldPgVersion != newPgVersion
		jobName, err := StartDBFinalizeJob(
			namespace, serverImage, flags.Image.PullPolicy, pullSecret, schemaUpdateRequired, isMigration,
		)
		if err != nil {
			return err
		}

		// Wait for ever for the job to finish: the duration of this job depends on the amount of data to reindex
		if err := kubernetes.WaitForJob(namespace, jobName, -1); err != nil {
			return err
		}

		// Run the Post Upgrade job
		jobName, err = StartPostUpgradeJob(namespace, serverImage, flags.Image.PullPolicy, pullSecret)
		if err != nil {
			return err
		}

		if err := kubernetes.WaitForJob(namespace, jobName, 60); err != nil {
			return err
		}
	}

	// Extract some data from the cluster to guess how to configure Uyuni.
	clusterInfos, err := kubernetes.CheckCluster()
	if err != nil {
		return err
	}

	if replicas := kubernetes.GetReplicas(namespace, ServerDeployName); replicas > 0 && !flags.HubXmlrpc.IsChanged {
		// Upgrade: detect the number of existing hub xmlrpc replicas
		flags.HubXmlrpc.Replicas = replicas
	}
	needsHub := flags.HubXmlrpc.Replicas > 0

	// Install the traefik / nginx config on the node
	// This will never be done in an operator.
	if err := deployNodeConfig(namespace, clusterInfos, needsHub, flags.Installation.Debug.Java); err != nil {
		return err
	}

	// Deploy the SSL CA and server certificates
	var caIssuer string
	if flags.Installation.SSL.UseExisting() {
		if err := DeployExistingCertificate(flags.Kubernetes.Uyuni.Namespace, &flags.Installation.SSL); err != nil {
			return err
		}
	} else if !HasIssuer(namespace, kubernetes.CaIssuerName) {
		// cert-manager is not required for 3rd party certificates, only if we have the CA key.
		// Note that in an operator we won't be able to install cert-manager and just wait for it to be installed.
		kubeconfig := clusterInfos.GetKubeconfig()

		if err := InstallCertManager(&flags.Kubernetes, kubeconfig, flags.Image.PullPolicy); err != nil {
			return utils.Errorf(err, L("cannot install cert manager"))
		}

		if flags.Installation.SSL.UseMigratedCa() {
			// Convert CA to RSA to use in a Kubernetes TLS secret.
			// In an operator we would have to fail now if there is no SSL password as we cannot prompt it.
			rootCA, err := os.ReadFile(flags.Installation.SSL.Ca.Root)
			if err != nil {
				return utils.Errorf(err, L("failed to read Root CA file"))
			}
			ca := types.SSLPair{
				Key: base64.StdEncoding.EncodeToString(
					ssl.GetRsaKey(flags.Installation.SSL.Ca.Key, flags.Installation.SSL.Password),
				),
				Cert: base64.StdEncoding.EncodeToString(ssl.StripTextFromCertificate(string(rootCA))),
			}

			// Install the cert-manager issuers
			if err := DeployReusedCa(namespace, &ca); err != nil {
				return err
			}
		} else {
			if err := DeployGeneratedCa(flags.Kubernetes.Uyuni.Namespace, &flags.Installation.SSL, fqdn); err != nil {
				return err
			}
		}

		// Wait for issuer to be ready
		if err := waitForIssuer(flags.Kubernetes.Uyuni.Namespace, kubernetes.CaIssuerName); err != nil {
			return err
		}

		// Extract the CA cert into uyuni-ca config map as the container shouldn't have the CA secret
		if err := extractCaCertToConfig(flags.Kubernetes.Uyuni.Namespace); err != nil {
			return err
		}
		caIssuer = kubernetes.CaIssuerName
	}

	// Create the Ingress routes before the deployments as those are triggering
	// the creation of the uyuni-cert secret from cert-manager.
	if err := CreateIngress(namespace, fqdn, caIssuer, clusterInfos.Ingress); err != nil {
		return err
	}

	// Wait for uyuni-cert secret to be ready
	kubernetes.WaitForSecret(namespace, CertSecretName)

	// Start the server
	if err := CreateServerDeployment(
		namespace, serverImage, flags.Image.PullPolicy, flags.Installation.TZ, flags.Installation.Debug.Java,
		flags.Volumes.Mirror, pullSecret,
	); err != nil {
		return err
	}

	// Create the services
	if err := CreateServices(namespace, flags.Installation.Debug.Java); err != nil {
		return err
	}

	if clusterInfos.Ingress == "traefik" {
		// Create the Traefik routes
		if err := CreateTraefikRoutes(namespace, needsHub, flags.Installation.Debug.Java); err != nil {
			return err
		}
	}

	// Wait for the server deployment to have a running pod before trying to set it up.
	if err := kubernetes.WaitForRunningDeployment(namespace, ServerDeployName); err != nil {
		return err
	}

	// Run the setup only if it hasn't be done before: this is a one-off task.
	// TODO Ideally we would need a job running at an earlier stage to persist the logs in a kubernetes-friendly way.
	if neverSetup(namespace, serverImage, flags.Image.PullPolicy, pullSecret) {
		if err := adm_utils.RunSetup(
			cnx, &flags.ServerFlags, fqdn, map[string]string{"NO_SSL": "Y"},
		); err != nil {
			if stopErr := kubernetes.Stop(namespace, kubernetes.ServerApp); stopErr != nil {
				log.Error().Msgf(L("Failed to stop service: %v"), stopErr)
			}
			return err
		}
	}

	// Store the DB credentials in a secret.
	if flags.Installation.DB.User != "" && flags.Installation.DB.Password != "" {
		if err := CreateDBSecret(
			namespace, DBSecret, flags.Installation.DB.User, flags.Installation.DB.Password,
		); err != nil {
			return err
		}
	}

	deploymentsStarting := []string{}

	// Start the Coco Deployments if requested.
	if replicas := kubernetes.GetReplicas(namespace, CocoDeployName); replicas != 0 && !flags.Coco.IsChanged {
		// Upgrade: detect the number of running coco replicas
		flags.Coco.Replicas = replicas
	}
	if flags.Coco.Replicas > 0 {
		cocoImage, err := utils.ComputeImage(flags.Image.Registry, flags.Image.Tag, flags.Coco.Image)
		if err != nil {
			return err
		}
		if err := StartCocoDeployment(
			namespace, cocoImage, flags.Image.PullPolicy, pullSecret, flags.Coco.Replicas,
			flags.Installation.DB.Port, flags.Installation.DB.Name,
		); err != nil {
			return err
		}
		deploymentsStarting = append(deploymentsStarting, CocoDeployName)
	}

	// In an operator mind, the user would just change the custom resource to enable the feature.
	if needsHub {
		// Install Hub API deployment, service
		hubAPIImage, err := utils.ComputeImage(flags.Image.Registry, flags.Image.Tag, flags.HubXmlrpc.Image)
		if err != nil {
			return err
		}
		if err := InstallHubAPI(namespace, hubAPIImage, flags.Image.PullPolicy, pullSecret); err != nil {
			return err
		}
		deploymentsStarting = append(deploymentsStarting, HubAPIDeployName)
	}

	// Wait for all the other deployments to be ready
	if err := kubernetes.WaitForDeployments(namespace, deploymentsStarting...); err != nil {
		return err
	}

	return nil
}