//Copyright (c) 2024 Shawn LoPresto
//This source code is licensed under the MIT license found in the
//LICENSE file in the root directory of this source tree.

package config

// OUConfig defines the structure for an Organizational Unit
type OUConfig struct {
	Name     string               `json:"name"`               // Name of the OU
	Children map[string]*OUConfig `json:"children,omitempty"` // Nested OUs, omitted if empty
}

// LandingZoneConfig defines the complete AWS Control Tower Landing Zone configuration
type LandingZoneConfig struct {
	GovernedRegions   []string             `json:"governedRegions"`   // Regions where Control Tower will be active
	DefaultOUName     string               `json:"defaultOUName"`     // Default OU for new accounts
	OrganizationUnits map[string]*OUConfig `json:"organizationUnits"` // Map of all OUs in the organization
	LogBucketName     string               `json:"logBucketName"`     // S3 bucket name for CloudTrail logs
	LogRetentionDays  int                  `json:"logRetentionDays"`  // Days to retain CloudWatch logs
	Tags              map[string]string    `json:"tags"`              // Resource tags
	KMSKeyAlias       string               `json:"kmsKeyAlias"`       // KMS key alias for encryption

	// Account configurations
	AccountEmailDomain  string `json:"accountEmailDomain"`  // Domain for account emails
	ManagementAccountId string `json:"managementAccountId"` // AWS Management account ID
	LogArchiveAccountId string `json:"logArchiveAccountId"` // Log Archive account ID
	AuditAccountId      string `json:"auditAccountId"`      // Audit account ID

	// Control Tower configurations
	KMSKeyArn         string   `json:"kmsKeyArn"`         // KMS key ARN for encryption
	CloudTrailRoleArn string   `json:"cloudTrailRoleArn"` // IAM role for CloudTrail
	EnabledGuardrails []string `json:"enabledGuardrails"` // List of guardrails to enable
	HomeRegion        string   `json:"homeRegion"`        // Primary Control Tower region
	AllowedRegions    []string `json:"allowedRegions"`    // Regions where accounts can operate

	// Network configurations
	VPCSettings *VPCConfig `json:"vpcSettings,omitempty"` // VPC configuration
}

// VPCConfig defines the VPC configuration settings
type VPCConfig struct {
	CIDR            string   `json:"cidr"`            // VPC CIDR range
	EnableTransitGW bool     `json:"enableTransitGw"` // Enable Transit Gateway
	AllowedRegions  []string `json:"allowedRegions"`  // Regions where VPC can be deployed
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
	VPCSettings: &VPCConfig{
		CIDR:            "10.0.0.0/8",
		EnableTransitGW: true,
		AllowedRegions:  []string{"us-east-1", "us-west-2"},
	},
}
