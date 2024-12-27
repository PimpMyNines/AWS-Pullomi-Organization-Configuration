package config

type OUConfig struct {
	Name     string               `json:"name"`
	Children map[string]*OUConfig `json:"children,omitempty"`
}

type LandingZoneConfig struct {
	GovernedRegions   []string             `json:"governedRegions"`
	DefaultOUName     string               `json:"defaultOUName"`
	OrganizationUnits map[string]*OUConfig `json:"organizationUnits"`
	LogRetentionDays  int                  `json:"logRetentionDays"`
	Tags              map[string]string    `json:"tags"`
	KMSKeyAlias       string               `json:"kmsKeyAlias"`
}

// Default configuration values
var DefaultConfig = LandingZoneConfig{
	GovernedRegions:  []string{"us-east-1", "us-west-2"},
	DefaultOUName:    "Sandbox",
	LogRetentionDays: 60,
	KMSKeyAlias:      "alias/controltower-key",
	Tags: map[string]string{
		"ManagedBy": "Pulumi",
		"Project":   "ControlTower",
	},
}
