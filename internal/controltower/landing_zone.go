//Copyright (c) 2024 Shawn LoPresto
//This source code is licensed under the MIT license found in the
//LICENSE file in the root directory of this source tree.

package controltower

import (
	"encoding/json"
	"fmt"
	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/config"
	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/organization"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/cloudtrail"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/cloudwatch"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/kms"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ssm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type LandingZoneManifest struct {
	GovernedRegions       []string               `json:"governedRegions"`
	OrganizationStructure map[string]interface{} `json:"organizationStructure"`
	CentralizedLogging    struct {
		AccountId      string `json:"accountId"`
		CloudTrailRole string `json:"cloudTrailRole"`
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
	if err != nil {
		return err
	}

	// Create CloudTrail role
	cloudTrailRole, err := iam.NewRole(ctx, "AWSControlTowerCloudTrail", &iam.RoleArgs{
		Name:        pulumi.String("AWSControlTowerCloudTrail"),
		Path:        pulumi.String("/service-role/"),
		Description: pulumi.String("Role for AWS Control Tower CloudTrail"),
		AssumeRolePolicy: pulumi.String(`{
            "Version": "2012-10-17",
            "Statement": [{
                "Effect": "Allow",
                "Principal": {
                    "Service": "cloudtrail.amazonaws.com"
                },
                "Action": "sts:AssumeRole"
            }]
        }`),
		Tags: pulumi.ToStringMap(tags),
	})
	if err != nil {
		return err
	}

	_, err = iam.NewRolePolicyAttachment(ctx, "cloudtrail-policy", &iam.RolePolicyAttachmentArgs{
		Role:      cloudTrailRole.Name,
		PolicyArn: pulumi.String("arn:aws:iam::aws:policy/service-role/AWSCloudTrailServiceRolePolicy"),
	})
	if err != nil {
		return err
	}

	// Create StackSet role
	stackSetRole, err := iam.NewRole(ctx, "AWSControlTowerStackSet", &iam.RoleArgs{
		Name:        pulumi.String("AWSControlTowerStackSet"),
		Path:        pulumi.String("/service-role/"),
		Description: pulumi.String("Role for AWS Control Tower StackSet operations"),
		AssumeRolePolicy: pulumi.String(`{
            "Version": "2012-10-17",
            "Statement": [{
                "Effect": "Allow",
                "Principal": {
                    "Service": "cloudformation.amazonaws.com"
                },
                "Action": "sts:AssumeRole"
            }]
        }`),
		Tags: pulumi.ToStringMap(tags),
	})
	if err != nil {
		return err
	}

	_, err = iam.NewRolePolicyAttachment(ctx, "stackset-admin-policy", &iam.RolePolicyAttachmentArgs{
		Role:      stackSetRole.Name,
		PolicyArn: pulumi.String("arn:aws:iam::aws:policy/AWSCloudFormationFullAccess"),
	})

	return err
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
	manifest.CentralizedLogging.CloudTrailRole = config.CloudTrailRoleArn
	manifest.CentralizedLogging.Configurations.LoggingBucket.RetentionDays = config.LogRetentionDays
	manifest.CentralizedLogging.Configurations.AccessLoggingBucket.RetentionDays = config.LogRetentionDays
	manifest.AccessManagement.Enabled = true

	return manifest, nil
}

func EnableGuardrails(ctx *pulumi.Context, config *config.LandingZoneConfig) error {
	mandatoryGuardrails := []struct {
		name        string
		description string
		policyDoc   string
	}{
		{
			name:        "require-mfa",
			description: "Requires MFA for IAM users",
			policyDoc: `{
                "Version": "2012-10-17",
                "Statement": [{
                    "Sid": "RequireMFA",
                    "Effect": "Deny",
                    "NotAction": [
                        "iam:CreateVirtualMFADevice",
                        "iam:EnableMFADevice",
                        "iam:GetUser",
                        "iam:ListMFADevices",
                        "iam:ListVirtualMFADevices",
                        "iam:ResyncMFADevice"
                    ],
                    "Resource": "*",
                    "Condition": {
                        "BoolIfExists": {
                            "aws:MultiFactorAuthPresent": "false"
                        }
                    }
                }]
            }`,
		},
	}

	for _, guardrail := range mandatoryGuardrails {
		_, err := iam.NewPolicy(ctx, guardrail.name, &iam.PolicyArgs{
			Description: pulumi.String(guardrail.description),
			Policy:      pulumi.String(guardrail.policyDoc),
			Tags:        pulumi.ToStringMap(config.Tags),
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func ConfigureNetworking(ctx *pulumi.Context, config *config.LandingZoneConfig) error {
	flowLogsRole, err := iam.NewRole(ctx, "VPCFlowLogsRole", &iam.RoleArgs{
		Name:        pulumi.String("AWSControlTowerVPCFlowLogs"),
		Description: pulumi.String("Role for VPC Flow Logs"),
		AssumeRolePolicy: pulumi.String(`{
            "Version": "2012-10-17",
            "Statement": [{
                "Effect": "Allow",
                "Principal": {
                    "Service": "vpc-flow-logs.amazonaws.com"
                },
                "Action": "sts:AssumeRole"
            }]
        }`),
		Tags: pulumi.ToStringMap(config.Tags),
	})
	if err != nil {
		return err
	}

	_, err = iam.NewRolePolicy(ctx, "vpc-flow-logs-policy", &iam.RolePolicyArgs{
		Role: flowLogsRole.Name,
		Policy: pulumi.String(`{
            "Version": "2012-10-17",
            "Statement": [{
                "Effect": "Allow",
                "Action": [
                    "logs:CreateLogGroup",
                    "logs:CreateLogStream",
                    "logs:PutLogEvents",
                    "logs:DescribeLogGroups",
                    "logs:DescribeLogStreams"
                ],
                "Resource": "*"
            }]
        }`),
	})

	return err
}

func ConfigureLogging(ctx *pulumi.Context, config *config.LandingZoneConfig) error {
	logGroup, err := cloudwatch.NewLogGroup(ctx, "cloudtrail-logs", &cloudwatch.LogGroupArgs{
		Name:            pulumi.String("/aws/controltower/cloudtrail"),
		RetentionInDays: pulumi.Int(config.LogRetentionDays),
		KmsKeyId:        pulumi.String(config.KMSKeyArn),
		Tags:            pulumi.ToStringMap(config.Tags),
	})
	if err != nil {
		return err
	}

	_, err = cloudtrail.NewTrail(ctx, "organization-trail", &cloudtrail.TrailArgs{
		Name:                       pulumi.String("aws-controltower-trail"),
		S3BucketName:               pulumi.String(config.LogBucketName),
		IncludeGlobalServiceEvents: pulumi.Bool(true),
		IsMultiRegionTrail:         pulumi.Bool(true),
		EnableLogging:              pulumi.Bool(true),
		CloudWatchLogsGroupArn:     logGroup.Arn,
		CloudWatchLogsRoleArn:      pulumi.String(config.CloudTrailRoleArn),
		KmsKeyId:                   pulumi.String(config.KMSKeyArn),
		Tags:                       pulumi.ToStringMap(config.Tags),
	})

	return err
}

func SetupLandingZone(ctx *pulumi.Context, org *organization.OrganizationSetup, config *config.LandingZoneConfig) error {
	keyPolicy := pulumi.String(fmt.Sprintf(`{
		"Version": "2012-10-17",
		"Statement": [
			{
				"Sid": "Enable IAM User Permissions",
				"Effect": "Allow",
				"Principal": {
					"AWS": "arn:aws:iam::%s:root"
				},
				"Action": "kms:*",
				"Resource": "*"
			},
			{
				"Sid": "Allow CloudTrail to encrypt logs",
				"Effect": "Allow",
				"Principal": {
					"Service": "cloudtrail.amazonaws.com"
				},
				"Action": [
					"kms:GenerateDataKey*",
					"kms:Decrypt"
				],
				"Resource": "*"
			},
			{
				"Sid": "Allow CloudWatch Logs to encrypt logs",
				"Effect": "Allow",
				"Principal": {
					"Service": "logs.amazonaws.com"
				},
				"Action": [
					"kms:Encrypt*",
					"kms:Decrypt*",
					"kms:ReEncrypt*",
					"kms:GenerateDataKey*",
					"kms:Describe*"
				],
				"Resource": "*"
			}
		]
	}`, config.ManagementAccountId))

	key, err := kms.NewKey(ctx, "controltower-key", &kms.KeyArgs{
		Description:       pulumi.String("KMS key for Control Tower"),
		EnableKeyRotation: pulumi.Bool(true),
		Policy:            keyPolicy,
		Tags:              pulumi.ToStringMap(config.Tags),
	})
	if err != nil {
		return err
	}

	_, err = kms.NewAlias(ctx, "controltower-key-alias", &kms.AliasArgs{
		Name:        pulumi.String(config.KMSKeyAlias),
		TargetKeyId: key.ID(),
	})
	if err != nil {
		return err
	}

	// Store the KMS key ARN in the config
	config.KMSKeyArn = key.Arn.ToStringOutput().ApplyT(func(arn string) string {
		return arn
	}).(pulumi.StringOutput).ToStringPtrOutput().Elem().String()

	if err = createControlTowerRoles(ctx, config.Tags); err != nil {
		return err
	}

	manifest, err := createLandingZoneManifest(config, org)
	if err != nil {
		return err
	}

	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return err
	}

	_, err = ssm.NewParameter(ctx, "/controltower/manifest", &ssm.ParameterArgs{
		Type:        pulumi.String("SecureString"),
		Value:       pulumi.String(string(manifestJSON)),
		Description: pulumi.String("Control Tower Landing Zone Manifest"),
		Tags:        pulumi.ToStringMap(config.Tags),
	})
	if err != nil {
		return err
	}

	if err := EnableGuardrails(ctx, config); err != nil {
		return err
	}

	if err := ConfigureNetworking(ctx, config); err != nil {
		return err
	}

	if err := ConfigureLogging(ctx, config); err != nil {
		return err
	}

	return nil
}
