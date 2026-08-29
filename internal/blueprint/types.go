package blueprint

// Blueprint is the subset of Pangolin's blueprint schema managed by this controller.
type Blueprint struct {
	PublicResources map[string]PublicResource `yaml:"public-resources"`
}

type PublicResource struct {
	Name       string   `yaml:"name"`
	Mode       string   `yaml:"mode"`
	FullDomain string   `yaml:"full-domain,omitempty"`
	ProxyPort  int32    `yaml:"proxy-port,omitempty"`
	Auth       *Auth    `yaml:"auth,omitempty"`
	Rules      []Rule   `yaml:"rules,omitempty"`
	Targets    []Target `yaml:"targets"`
}

type Target struct {
	Hostname string `yaml:"hostname"`
	Port     int32  `yaml:"port"`
	Method   string `yaml:"method,omitempty"`
}

type Auth struct {
	SSOEnabled     *bool    `yaml:"sso-enabled,omitempty"`
	SSORoles       []string `yaml:"sso-roles,omitempty"`
	SSOUsers       []string `yaml:"sso-users,omitempty"`
	WhitelistUsers []string `yaml:"whitelist-users,omitempty"`
}

type Rule struct {
	Action   string `json:"action" yaml:"action"`
	Match    string `json:"match" yaml:"match"`
	Value    string `json:"value" yaml:"value"`
	Priority *int   `json:"priority,omitempty" yaml:"priority,omitempty"`
}
