//Copyright (c) 2024 Shawn LoPresto
//This source code is licensed under the MIT license found in the
//LICENSE file in the root directory of this source tree.

package accounts

import (
	"fmt"
	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/config"
	"github.com/pulumi/pulumi-aws/sdk/v4/go/aws/organizations"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ssm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type AccountConfig struct {
	Name       string
	Email      string
	ParentOUId pulumi.StringInput
	Tags       map[string]string
}

func CreateAccount(ctx *pulumi.Context, accountConfig *AccountConfig) (*organizations.Account, error) {
	account, err := organizations.NewAccount(ctx, accountConfig.Name, &organizations.AccountArgs{
		Email:    pulumi.String(accountConfig.Email),
		Name:     pulumi.String(accountConfig.Name),
		ParentId: accountConfig.ParentOUId,
		RoleName: pulumi.String("OrganizationAccountAccessRole"),
		Tags:     pulumi.ToStringMap(accountConfig.Tags),
	})
	if err != nil {
		return nil, fmt.Errorf("error creating account %s: %v", accountConfig.Name, err)
	}

	// Store account information in SSM Parameter Store
	_, err = ssm.NewParameter(ctx, fmt.Sprintf("/organization/accounts/%s", accountConfig.Name), &ssm.ParameterArgs{
		Type: pulumi.String("SecureString"),
		Value: pulumi.All(account.Id, account.Arn).ApplyT(func(args []interface{}) string {
			return fmt.Sprintf(`{"id":"%s","arn":"%s","name":"%s"}`, args[0], args[1], accountConfig.Name)
		}).(pulumi.StringOutput),
		Description: pulumi.Sprintf("Information for Account: %s", accountConfig.Name),
		Tags:        pulumi.ToStringMap(accountConfig.Tags),
	})

	return account, err
}

func CreateDefaultAccounts(ctx *pulumi.Context, securityOUId pulumi.StringInput, config *config.LandingZoneConfig) error {
	// Create AFT-Management Account
	_, err := CreateAccount(ctx, &AccountConfig{
		Name:       "AFT-Management",
		Email:      "aft-management@yourdomain.com", // Should be configurable
		ParentOUId: securityOUId,
		Tags:       config.Tags,
	})
	if err != nil {
		return err
	}

	// Create AFT-Networking Account
	_, err = CreateAccount(ctx, &AccountConfig{
		Name:       "AFT-Networking",
		Email:      "aft-networking@yourdomain.com", // Should be configurable
		ParentOUId: securityOUId,
		Tags:       config.Tags,
	})
	if err != nil {
		return err
	}

	return nil
}

func RegisterAccountWithControlTower(ctx *pulumi.Context, accountId string, config *config.LandingZoneConfig) error {
	// Implementation would depend on AWS Control Tower API
	// This might require custom resource implementation or AWS SDK calls
	// As Control Tower doesn't have direct Pulumi provider support yet

	return nil
}
