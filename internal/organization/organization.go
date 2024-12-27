//Copyright (c) 2024 Shawn LoPresto
//This source code is licensed under the MIT license found in the
//LICENSE file in the root directory of this source tree.

package organization

import (
	"fmt"

	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/config"
	"github.com/pulumi/pulumi-aws/sdk/v4/go/aws/organizations"
	"github.com/pulumi/pulumi-aws/sdk/v4/go/aws/ssm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type OrganizationSetup struct {
	Org           *organizations.Organization
	SecurityOU    *organizations.OrganizationalUnit
	AdditionalOUs map[string]*organizations.OrganizationalUnit
	RootId        pulumi.StringOutput
}

type OUInfo struct {
	Id   string `json:"id"`
	Arn  string `json:"arn"`
	Name string `json:"name"`
}

// createOU creates an organizational unit and stores its info in SSM Parameter Store
func createOU(ctx *pulumi.Context, name string, parentId pulumi.StringInput, tags pulumi.StringMap) (*organizations.OrganizationalUnit, error) {
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
		Value: pulumi.All(ou.Id, ou.Arn).ApplyT(func(args []interface{}) string {
			return fmt.Sprintf(`{"id":"%s","arn":"%s","name":"%s"}`, args[0], args[1], name)
		}).(pulumi.StringOutput),
		Description: pulumi.Sprintf("Information for OU: %s", name),
		Tags:        tags,
	})

	return ou, err
}

// createOUHierarchy recursively creates the OU hierarchy based on the configuration
func createOUHierarchy(ctx *pulumi.Context, parent pulumi.StringInput, ouConfig *config.OUConfig,
	tags pulumi.StringMap) (*organizations.OrganizationalUnit, map[string]*organizations.OrganizationalUnit, error) {

	ou, err := createOU(ctx, ouConfig.Name, parent, tags)
	if err != nil {
		return nil, nil, err
	}

	ous := make(map[string]*organizations.OrganizationalUnit)
	ous[ouConfig.Name] = ou

	for _, child := range ouConfig.Children {
		childOU, childOUs, err := createOUHierarchy(ctx, ou.Id, child, tags)
		if err != nil {
			return nil, nil, err
		}
		for k, v := range childOUs {
			ous[k] = v
		}
	}

	return ou, ous, nil
}

func NewOrganization(ctx *pulumi.Context, config *config.LandingZoneConfig) (*OrganizationSetup, error) {
	// Create AWS Organization
	org, err := organizations.NewOrganization(ctx, "aws-org", &organizations.OrganizationArgs{
		FeatureSet: pulumi.String("ALL"),
		EnabledPolicyTypes: pulumi.StringArray{
			pulumi.String("SERVICE_CONTROL_POLICY"),
			pulumi.String("TAG_POLICY"),
		},
		AwsServiceAccessPrincipals: pulumi.StringArray{
			pulumi.String("controltower.amazonaws.com"),
			pulumi.String("sso.amazonaws.com"),
			pulumi.String("config.amazonaws.com"),
			pulumi.String("cloudtrail.amazonaws.com"),
		},
	}, pulumi.Tags(config.Tags))
	if err != nil {
		return nil, fmt.Errorf("error creating organization: %v", err)
	}

	// Get root ID
	rootId := org.RootsOutput.Index(pulumi.Int(0)).Id()

	// Create Security OU
	securityOU, err := createOU(ctx, "Security", rootId, pulumi.ToStringMap(config.Tags))
	if err != nil {
		return nil, err
	}

	// Create additional OUs based on configuration
	additionalOUs := make(map[string]*organizations.OrganizationalUnit)
	for _, ouConfig := range config.OrganizationUnits {
		_, ous, err := createOUHierarchy(ctx, rootId, ouConfig, pulumi.ToStringMap(config.Tags))
		if err != nil {
			return nil, err
		}
		for k, v := range ous {
			additionalOUs[k] = v
		}
	}

	return &OrganizationSetup{
		Org:           org,
		SecurityOU:    securityOU,
		AdditionalOUs: additionalOUs,
		RootId:        rootId,
	}, nil
}
