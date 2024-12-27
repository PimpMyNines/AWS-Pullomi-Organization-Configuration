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
	pulumi.Run(func(ctx *pulumi.Context) error {
		// Load configuration
		cfg := config.DefaultConfig
		cfg.OrganizationUnits = map[string]*config.OUConfig{
			"Workloads": {
				Name: "Workloads",
				Children: map[string]*config.OUConfig{
					"Development": {Name: "Development"},
					"Production":  {Name: "Production"},
				},
			},
		}

		// Setup Organization
		org, err := organization.NewOrganization(ctx, &cfg)
		if err != nil {
			return err
		}

		// Setup Control Tower Landing Zone
		err = controltower.SetupLandingZone(ctx, org, &cfg)
		if err != nil {
			return err
		}

		return nil
	})
}
