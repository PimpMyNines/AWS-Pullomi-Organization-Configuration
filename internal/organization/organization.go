//Copyright (c) 2024 Shawn LoPresto
//This source code is licensed under the MIT license found in the
//LICENSE file in the root directory of this source tree.

package organization

// ... existing code ...
import (
	"encoding/json"
	"fmt"
	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/config"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/organizations"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ssm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

const (
	SSMOrgInfoPath   = "/organization/info"
	SSMOUInfoPathFmt = "/organization/ou/%s"
	FeatureSetAll    = "ALL"
	PolicyTypeSCP    = "SERVICE_CONTROL_POLICY"
	PolicyTypeTag    = "TAG_POLICY"
)

// createOU creates an organizational unit and stores its info in SSM Parameter Store
func createOU(ctx *pulumi.Context, name string, parentId pulumi.StringInput, tags pulumi.StringMap) (*organizations.OrganizationalUnit, error) {
	if name == "" {
		return nil, fmt.Errorf("OU name cannot be empty")
	}
	if parentId == nil {
		return nil, fmt.Errorf("parent ID cannot be nil")
	}
	ou, err := organizations.NewOrganizationalUnit(ctx, name, &organizations.OrganizationalUnitArgs{
		Name:     pulumi.String(name),
		ParentId: parentId,
		Tags:     tags,
	})
	if err != nil {
		return nil, fmt.Errorf("error creating OU %s: %v", name, err)
	}

	// Store OU information in SSM Parameter Store
	_, err = ssm.NewParameter(ctx, fmt.Sprintf("/organization/ou/%s", name), &ssm.ParameterArgs{
		Type: pulumi.String("SecureString"),
		Value: pulumi.All(ou.ID(), ou.Arn).ApplyT(func(args []interface{}) (string, error) {
			ouInfo := config.OUInfo{
				Id:   args[0].(string),
				Arn:  args[1].(string),
				Name: name,
			}
			value, err := json.Marshal(ouInfo)
			if err != nil {
				return "", err
			}
			return string(value), nil
		}).(pulumi.StringOutput),
		Description: pulumi.Sprintf("Information for OU: %s", name),
		Tags:        tags,
	})
	if err != nil {
		return nil, fmt.Errorf("error creating SSM parameter for OU %s: %v", name, err)
	}

	return ou, nil
}

// createOUHierarchy recursively creates the OU hierarchy based on the configuration
func createOUHierarchy(ctx *pulumi.Context, parent pulumi.StringInput, ouConfig *config.OUConfig,
	tags pulumi.StringMap) (*organizations.OrganizationalUnit, map[string]*organizations.OrganizationalUnit, error) {

	ou, err := createOU(ctx, ouConfig.Name, parent, tags)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating OU hierarchy for %s: %v", ouConfig.Name, err)
	}

	ous := make(map[string]*organizations.OrganizationalUnit)
	ous[ouConfig.Name] = ou

	for _, child := range ouConfig.Children {
		_, childOUs, err := createOUHierarchy(ctx, ou.ID(), child, tags)
		if err != nil {
			return nil, nil, err
		}
		// Add all child OUs to the map
		for k, v := range childOUs {
			ous[k] = v
		}
	}

	return ou, ous, nil
}

// Modify NewOrganization function to use the correct config reference
func NewOrganization(ctx *pulumi.Context, cfg *config.OrganizationConfig) (*config.OrganizationSetup, error) {
	if cfg == nil || cfg.LandingZoneConfig == nil {
		return nil, fmt.Errorf("configuration cannot be nil")
	}
	// Create AWS Organization
	org, err := organizations.NewOrganization(ctx, "aws-org", &organizations.OrganizationArgs{
		FeatureSet: pulumi.String(FeatureSetAll),
		EnabledPolicyTypes: pulumi.StringArray{
			pulumi.String(PolicyTypeSCP),
			pulumi.String(PolicyTypeTag),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error creating organization: %v", err)
	}

	// Get root ID
	rootId := org.Roots.Index(pulumi.Int(0)).Id()

	// Create Security OU
	securityOU, err := createOU(ctx, "Security", rootId, pulumi.ToStringMap(cfg.LandingZoneConfig.Tags))
	if err != nil {
		return nil, err
	}

	// Create Default OU
	defaultOU, err := createOU(ctx, cfg.LandingZoneConfig.DefaultOUName, rootId, pulumi.ToStringMap(cfg.LandingZoneConfig.Tags))
	if err != nil {
		return nil, err
	}

	// Create additional OUs based on configuration
	additionalOUs := make(map[string]*organizations.OrganizationalUnit)
	for _, ouConfig := range cfg.LandingZoneConfig.OrganizationUnits {
		_, ous, err := createOUHierarchy(ctx, rootId, ouConfig, pulumi.ToStringMap(cfg.LandingZoneConfig.Tags))
		if err != nil {
			return nil, err
		}
		for k, v := range ous {
			additionalOUs[k] = v
		}
	}

	// Store organization information in SSM
	_, err = ssm.NewParameter(ctx, SSMOrgInfoPath, &ssm.ParameterArgs{
		Type: pulumi.String("SecureString"),
		Value: pulumi.All(org.ID(), org.Arn, rootId, defaultOU.ID(), securityOU.ID()).ApplyT(func(args []interface{}) (string, error) {
			orgInfo := config.OrganizationInfo{
				Id:            args[0].(string),
				Arn:           args[1].(string),
				RootId:        args[2].(string),
				DefaultOUId:   args[3].(string),
				SecurityOUId:  args[4].(string),
				AdditionalOUs: make(map[string]config.OUInfo),
			}
			value, err := json.Marshal(orgInfo)
			if err != nil {
				return "", fmt.Errorf("error marshaling organization info: %v", err)
			}
			return string(value), nil
		}).(pulumi.StringOutput),
		Description: pulumi.String("AWS Organization Information"),
		Tags:        pulumi.ToStringMap(cfg.LandingZoneConfig.Tags),
	})

	if err != nil {
		return nil, fmt.Errorf("error storing organization info: %v", err)
	}

	return &config.OrganizationSetup{
		Org:           org,
		SecurityOU:    securityOU,
		DefaultOU:     defaultOU,
		AdditionalOUs: additionalOUs,
		RootId:        pulumi.StringOutput(rootId),
	}, nil
}
