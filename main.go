package main

import (
	"github.com/pimpmynine/aws-controltower-module/internal/controltower"
	"github.com/pimpmynine/aws-controltower-module/internal/organization"
	"github.com/pimpmynines/aws-controltower-module/internal/config"
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
