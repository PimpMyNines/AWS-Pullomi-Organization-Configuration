//Copyright (c) 2024 Shawn LoPresto
//This source code is licensed under the MIT license found in the
//LICENSE file in the root directory of this source tree.

package main

import (
	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/config"
	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/controltower"
	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/organization"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func main() {
	// Pulumi.Run executes the deployment logic and handles the infrastructure lifecycle
	pulumi.Run(func(ctx *pulumi.Context) error {
		// Initialize configuration with default values
		cfg := config.DefaultConfig

		// Define the AWS Organization structure
		// Creates a hierarchical structure with:
		// - Root OU
		//   - Workloads OU
		//     - Development OU (for dev/test environments)
		//     - Production OU (for prod workloads)
		cfg.OrganizationUnits = map[string]*config.OUConfig{
			"Workloads": {
				Name: "Workloads",
				Children: map[string]*config.OUConfig{
					"Development": {Name: "Development"},
					"Production":  {Name: "Production"},
				},
			},
		}

		// Create and configure the AWS Organization
		// This sets up the basic organization structure including:
		// - Organization creation if it doesn't exist
		// - OU hierarchy creation
		// - Service principal access configuration
		org, err := organization.NewOrganization(ctx, &cfg)
		if err != nil {
			return pulumi.Error(err) // Properly wrap error for Pulumi
		}

		// Initialize AWS Control Tower Landing Zone
		// This configures governance and compliance including:
		// - Security baseline
		// - Compliance standards
		// - Account factory setup
		// - Guardrails deployment
		if err := controltower.SetupLandingZone(ctx, org, &cfg); err != nil {
			return pulumi.Error(err) // Properly wrap error for Pulumi
		}

		return nil
	})
}
