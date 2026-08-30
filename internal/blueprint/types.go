package blueprint

// Blueprint is the subset of Pangolin's blueprint schema managed by this controller.
type Blueprint struct {
	PublicResources map[string]PublicResource `yaml:"public-resources"`
	PublicPolicies  map[string]PublicPolicy   `yaml:"public-policies,omitempty"`
}

type PublicResource struct {
	Name       string   `yaml:"name"`
	Mode       string   `yaml:"mode"`
	Policy     string   `yaml:"policy,omitempty"`
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

type PublicPolicy struct {
	Name                  string     `yaml:"name"`
	SSO                   bool       `yaml:"sso"`
	AutoLoginIDP          *int       `yaml:"auto-login-idp,omitempty"`
	SSORoles              []string   `yaml:"sso-roles,omitempty"`
	SSOUsers              []string   `yaml:"sso-users,omitempty"`
	Password              string     `yaml:"password,omitempty"`
	PINCode               string     `yaml:"pincode,omitempty"`
	BasicAuth             *BasicAuth `yaml:"basic-auth,omitempty"`
	EmailWhitelistEnabled bool       `yaml:"email-whitelist-enabled"`
	WhitelistUsers        []string   `yaml:"whitelist-users,omitempty"`
	ApplyRules            bool       `yaml:"apply-rules"`
	Rules                 []Rule     `yaml:"rules,omitempty"`
}

type BasicAuth struct {
	User                  string `yaml:"user"`
	Password              string `yaml:"password"`
	ExtendedCompatibility *bool  `yaml:"extended-compatibility,omitempty"`
}

type Rule struct {
	Action   string `json:"action" yaml:"action"`
	Match    string `json:"match" yaml:"match"`
	Value    string `json:"value" yaml:"value"`
	Priority *int   `json:"priority,omitempty" yaml:"priority,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty" yaml:"enabled,omitempty"`
}
