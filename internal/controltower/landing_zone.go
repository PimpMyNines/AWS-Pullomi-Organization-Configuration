//Copyright (c) 2024 Shawn LoPresto
//This source code is licensed under the MIT license found in the
//LICENSE file in the root directory of this source tree.

package controltower

import (
	"encoding/json"
	"fmt"

	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/config"
	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/organization"
	"github.com/pulumi/pulumi-aws/sdk/v4/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v4/go/aws/kms"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ssm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type LandingZoneManifest struct {
	GovernedRegions       []string               `json:"governedRegions"`
	OrganizationStructure map[string]interface{} `json:"organizationStructure"`
	CentralizedLogging    struct {
		AccountId      string `json:"accountId"`
		Configurations struct {
			LoggingBucket struct {
				RetentionDays int `json:"retentionDays"`
			} `json:"loggingBucket"`
			AccessLoggingBucket struct {
				RetentionDays int `json:"retentionDays"`
			} `json:"accessLoggingBucket"`
		} `json:"configurations"`
		Enabled bool `json:"enabled"`
	} `json:"centralizedLogging"`
	SecurityRoles struct {
		AccountId string `json:"accountId"`
	} `json:"securityRoles"`
	AccessManagement struct {
		Enabled bool `json:"enabled"`
	} `json:"accessManagement"`
}

func createControlTowerRoles(ctx *pulumi.Context, tags map[string]string) error {
	// Create AWSControlTowerAdmin role
	adminRole, err := iam.NewRole(ctx, "AWSControlTowerAdmin", &iam.RoleArgs{
		Name:        pulumi.String("AWSControlTowerAdmin"),
		Path:        pulumi.String("/service-role/"),
		Description: pulumi.String("Role for AWS Control Tower administration"),
		AssumeRolePolicy: pulumi.String(`{
			"Version": "2012-10-17",
			"Statement": [{
				"Effect": "Allow",
				"Principal": {
					"Service": "controltower.amazonaws.com"
				},
				"Action": "sts:AssumeRole"
			}]
		}`),
		Tags: pulumi.ToStringMap(tags),
	})
	if err != nil {
		return err
	}

	// Attach necessary policies
	_, err = iam.NewRolePolicyAttachment(ctx, "control-tower-service-policy", &iam.RolePolicyAttachmentArgs{
		Role:      adminRole.Name,
		PolicyArn: pulumi.String("arn:aws:iam::aws:policy/service-role/AWSControlTowerServiceRolePolicy"),
	})

	// Create other required roles (CloudTrail, StackSet, ConfigAggregator)
	// ... Similar implementation for other roles ...

	return nil
}

func createLandingZoneManifest(config *config.LandingZoneConfig, org *organization.OrganizationSetup) (*LandingZoneManifest, error) {
	manifest := &LandingZoneManifest{
		GovernedRegions: config.GovernedRegions,
		OrganizationStructure: map[string]interface{}{
			"security": map[string]string{
				"name": "Security",
			},
			"sandbox": map[string]string{
				"name": config.DefaultOUName,
			},
		},
	}

	manifest.CentralizedLogging.Enabled = true
	manifest.CentralizedLogging.Configurations.LoggingBucket.RetentionDays = config.LogRetentionDays
	manifest.CentralizedLogging.Configurations.AccessLoggingBucket.RetentionDays = config.LogRetentionDays
	manifest.AccessManagement.Enabled = true

	return manifest, nil
}

func SetupLandingZone(ctx *pulumi.Context, org *organization.OrganizationSetup, config *config.LandingZoneConfig) error {
	// Create KMS Key for Control Tower
	key, err := kms.NewKey(ctx, "controltower-key", &kms.KeyArgs{
		Description:       pulumi.String("KMS key for Control Tower"),
		EnableKeyRotation: pulumi.Bool(true),
		Tags:              pulumi.ToStringMap(config.Tags),
	})
	if err != nil {
		return err
	}

	// Create KMS Alias
	_, err = kms.NewAlias(ctx, "controltower-key-alias", &kms.AliasArgs{
		Name:        pulumi.String(config.KMSKeyAlias),
		TargetKeyId: key.ID(),
	})
	if err != nil {
		return err
	}

	// Create required IAM roles
	err = createControlTowerRoles(ctx, config.Tags)
	if err != nil {
		return err
	}

	// Create landing zone manifest
	manifest, err := createLandingZoneManifest(config, org)
	if err != nil {
		return err
	}

	// Convert manifest to JSON
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return err
	}

	// Store manifest in SSM Parameter Store
	_, err = ssm.NewParameter(ctx, "/controltower/manifest", &ssm.ParameterArgs{
		Type:        pulumi.String("SecureString"),
		Value:       pulumi.String(string(manifestJSON)),
		Description: pulumi.String("Control Tower Landing Zone Manifest"),
		Tags:        pulumi.ToStringMap(config.Tags),
	})

	return err
}
