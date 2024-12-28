//Copyright (c) 2024 Shawn LoPresto
//This source code is licensed under the MIT license found in the
//LICENSE file in the root directory of this source tree.

package config

import (
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/organizations"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type OrganizationConfig struct {
	AWSProfile        string
	LandingZoneConfig *LandingZoneConfig
}

type OrganizationSetup struct {
	Org           *organizations.Organization
	SecurityOU    *organizations.OrganizationalUnit
	DefaultOU     *organizations.OrganizationalUnit
	AdditionalOUs map[string]*organizations.OrganizationalUnit
	RootId        pulumi.StringOutput
	cleanup       []func() error
}

type OUInfo struct {
	Id   string `json:"id"`
	Arn  string `json:"arn"`
	Name string `json:"name"`
}

type OrganizationInfo struct {
	Id            string            `json:"id"`
	Arn           string            `json:"arn"`
	RootId        string            `json:"rootId"`
	DefaultOUId   string            `json:"defaultOUId"`
	SecurityOUId  string            `json:"securityOUId"`
	AdditionalOUs map[string]OUInfo `json:"additionalOUs"`
}

// OUConfig defines the structure for an Organizational Unit
type OUConfig struct {
	Name     string               `json:"name"`               // Name of the OU
	Children map[string]*OUConfig `json:"children,omitempty"` // Nested OUs, omitted if empty
}

// LandingZoneConfig defines the complete AWS Control Tower Landing Zone configuration
type LandingZoneConfig struct {
	// Basic configurations
	GovernedRegions   []string             `json:"governedRegions"`   // Regions where Control Tower will be active
	DefaultOUName     string               `json:"defaultOUName"`     // Default OU for new accounts
	OrganizationUnits map[string]*OUConfig `json:"organizationUnits"` // Map of all OUs in the organization
	LogBucketName     string               `json:"logBucketName"`     // S3 bucket name for CloudTrail logs
	LogRetentionDays  int                  `json:"logRetentionDays"`  // Days to retain CloudWatch logs
	Tags              map[string]string    `json:"tags"`              // Resource tags

	// Encryption configurations
	KMSKeyAlias string `json:"kmsKeyAlias"` // KMS key alias for encryption
	KMSKeyArn   string `json:"kmsKeyArn"`   // KMS key ARN for encryption
	KMSKeyId    string `json:"kmsKeyId"`    // KMS key ID

	// Account configurations
	AccountEmailDomain  string `json:"accountEmailDomain"`  // Domain for account emails
	ManagementAccountId string `json:"managementAccountId"` // AWS Management account ID
	LogArchiveAccountId string `json:"logArchiveAccountId"` // Log Archive account ID
	AuditAccountId      string `json:"auditAccountId"`      // Audit account ID
	SecurityAccountId   string `json:"securityAccountId"`   // Security account ID

	// Control Tower configurations
	CloudTrailRoleArn   string   `json:"cloudTrailRoleArn"`   // IAM role for CloudTrail
	EnabledGuardrails   []string `json:"enabledGuardrails"`   // List of guardrails to enable
	HomeRegion          string   `json:"homeRegion"`          // Primary Control Tower region
	AllowedRegions      []string `json:"allowedRegions"`      // Regions where accounts can operate
	ManagementRoleArn   string   `json:"managementRoleArn"`   // IAM role for Control Tower management
	StackSetRoleArn     string   `json:"stackSetRoleArn"`     // IAM role for StackSet operations
	CloudWatchRoleArn   string   `json:"cloudWatchRoleArn"`   // IAM role for CloudWatch
	VPCFlowLogsRoleArn  string   `json:"vpcFlowLogsRoleArn"`  // IAM role for VPC Flow Logs
	OrganizationRoleArn string   `json:"organizationRoleArn"` // IAM role for Organization management

	// Logging configurations
	CloudWatchLogGroup     string `json:"cloudWatchLogGroup"`     // CloudWatch Log Group name
	CloudTrailLogGroup     string `json:"cloudTrailLogGroup"`     // CloudTrail Log Group name
	CloudTrailBucketRegion string `json:"cloudTrailBucketRegion"` // Region for CloudTrail bucket
	AccessLogBucketName    string `json:"accessLogBucketName"`    // S3 bucket for access logs
	FlowLogBucketName      string `json:"flowLogBucketName"`      // S3 bucket for VPC flow logs

	// Network configurations
	VPCSettings *VPCConfig `json:"vpcSettings,omitempty"` // VPC configuration

	// Security configurations
	RequireMFA         bool     `json:"requireMFA"`         // Require MFA for IAM users
	EnableSSLRequests  bool     `json:"enableSSLRequests"`  // Require SSL for API requests
	EnableSecurityHub  bool     `json:"enableSecurityHub"`  // Enable AWS Security Hub
	EnableGuardDuty    bool     `json:"enableGuardDuty"`    // Enable AWS GuardDuty
	EnableConfig       bool     `json:"enableConfig"`       // Enable AWS Config
	EnableCloudTrail   bool     `json:"enableCloudTrail"`   // Enable AWS CloudTrail
	AllowedIPRanges    []string `json:"allowedIPRanges"`    // Allowed IP ranges for API access
	RestrictedServices []string `json:"restrictedServices"` // AWS services to restrict
}

// VPCConfig defines the VPC configuration settings
type VPCConfig struct {
	CIDR               string            `json:"cidr"`               // VPC CIDR range
	EnableTransitGW    bool              `json:"enableTransitGw"`    // Enable Transit Gateway
	AllowedRegions     []string          `json:"allowedRegions"`     // Regions where VPC can be deployed
	Subnets            []SubnetConfig    `json:"subnets"`            // Subnet configurations
	EnableVPCFlowLogs  bool              `json:"enableVPCFlowLogs"`  // Enable VPC Flow Logs
	EnableDNSHostnames bool              `json:"enableDNSHostnames"` // Enable DNS hostnames
	EnableDNSSupport   bool              `json:"enableDNSSupport"`   // Enable DNS support
	Tags               map[string]string `json:"tags"`               // VPC specific tags
}

// SubnetConfig defines the configuration for VPC subnets
type SubnetConfig struct {
	CIDR             string            `json:"cidr"`             // Subnet CIDR range
	Name             string            `json:"name"`             // Subnet name
	Type             string            `json:"type"`             // Subnet type (public/private)
	AvailabilityZone string            `json:"availabilityZone"` // AZ for subnet
	Tags             map[string]string `json:"tags"`             // Subnet specific tags
}

// DefaultConfig provides default values for LandingZoneConfig
var DefaultConfig = LandingZoneConfig{
	GovernedRegions:   []string{"us-east-1", "us-west-2"},
	DefaultOUName:     "Sandbox",
	OrganizationUnits: map[string]*OUConfig{},
	LogBucketName:     "aws-controltower-logs",
	LogRetentionDays:  60,
	Tags: map[string]string{
		"ManagedBy": "Pulumi",
		"Project":   "ControlTower",
	},
	KMSKeyAlias:        "alias/controltower-key",
	AccountEmailDomain: "example.com",
	HomeRegion:         "us-east-1",
	AllowedRegions:     []string{"us-east-1", "us-west-2"},
	EnabledGuardrails:  []string{"REQUIRED", "STRONGLY_RECOMMENDED"},

	// Default security settings
	RequireMFA:        true,
	EnableSSLRequests: true,
	EnableSecurityHub: true,
	EnableGuardDuty:   true,
	EnableConfig:      true,
	EnableCloudTrail:  true,

	// Default logging settings
	CloudWatchLogGroup: "/aws/controltower",
	CloudTrailLogGroup: "/aws/controltower/cloudtrail",

	// Default VPC settings
	VPCSettings: &VPCConfig{
		CIDR:               "10.0.0.0/8",
		EnableTransitGW:    true,
		AllowedRegions:     []string{"us-east-1", "us-west-2"},
		EnableVPCFlowLogs:  true,
		EnableDNSHostnames: true,
		EnableDNSSupport:   true,
		Tags: map[string]string{
			"ManagedBy": "Pulumi",
			"Network":   "Primary",
		},
		Subnets: []SubnetConfig{
			{
				CIDR: "10.0.0.0/24",
				Name: "Public-1",
				Type: "public",
				Tags: map[string]string{
					"Type": "Public",
				},
			},
			{
				CIDR: "10.0.1.0/24",
				Name: "Private-1",
				Type: "private",
				Tags: map[string]string{
					"Type": "Private",
				},
			},
		},
	},
}

func (o *OrganizationSetup) Cleanup() error {
	var lastErr error
	for _, cleanup := range o.cleanup {
		if err := cleanup(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}
