package config

import "testing"

func TestValidateProfile_OK(t *testing.T) {
	t.Parallel()
	p := Profile{
		Name:              "work",
		Host:              "192.168.1.20",
		User:              "jdoe",
		Domain:            "CORP",
		Client:            DefaultClient,
		Size:              "100%",
		Fullscreen:        false,
		DynamicResolution: true,
		Scale:             100,
	}
	if err := ValidateProfile(p); err != nil {
		t.Fatal(err)
	}
}

func TestValidateName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ok   bool
	}{
		{"work", true},
		{"", false},
		{" leading", false},
		{"trailing ", false},
		{"-dash", false},
		{"a\x00b", false},
		{"ok-name", true},
	}
	for _, tc := range cases {
		p := validProfile()
		p.Name = tc.name
		err := ValidateProfile(p)
		if tc.ok && err != nil {
			t.Errorf("name %q: %v", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("name %q: want error", tc.name)
		}
	}
}

func TestValidateHost(t *testing.T) {
	t.Parallel()
	ok := []string{"pc", "192.168.1.20", "pc:3389", "192.168.1.1:3389", "[::1]", "[::1]:3389", "[2001:db8::1]:3389"}
	bad := []string{"", "  x", "2001:db8::1", "2001:db8::1:3389", "[::1]:", "[::1]3389", ":3389", "pc:0", "pc:65536", "pc:nope"}
	for _, h := range ok {
		p := validProfile()
		p.Host = h
		if err := ValidateProfile(p); err != nil {
			t.Errorf("host %q: %v", h, err)
		}
	}
	for _, h := range bad {
		p := validProfile()
		p.Host = h
		if err := ValidateProfile(p); err == nil {
			t.Errorf("host %q: want error", h)
		}
	}
}

func TestValidateUserDomain(t *testing.T) {
	t.Parallel()
	p := validProfile()
	p.User = ""
	if err := ValidateProfile(p); err == nil {
		t.Fatal("empty user")
	}
	p = validProfile()
	p.Domain = "CORP"
	p.User = `CORP\jdoe`
	if err := ValidateProfile(p); err == nil {
		t.Fatal("user with backslash and domain")
	}
	p = validProfile()
	p.Domain = "CORP"
	p.User = "jdoe@corp"
	if err := ValidateProfile(p); err == nil {
		t.Fatal("user with @ and domain")
	}
	p = validProfile()
	p.Domain = ""
	p.User = `CORP\jdoe`
	if err := ValidateProfile(p); err != nil {
		t.Fatalf("backslash without domain: %v", err)
	}
}

func TestValidateClient(t *testing.T) {
	t.Parallel()
	p := validProfile()
	p.Client = "/tmp/x"
	if err := ValidateProfile(p); err == nil {
		t.Fatal("path client")
	}
	p.Client = "sdl freerdp"
	if err := ValidateProfile(p); err == nil {
		t.Fatal("whitespace client")
	}
	p.Client = "xfreerdp3"
	if err := ValidateProfile(p); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSize(t *testing.T) {
	t.Parallel()
	ok := []string{"", "1920x1080", "1920X1080", "100%", "1x1", "50%"}
	bad := []string{"nope", "0x1080", "1920x0", "0%", "x1080", "1920", "100", "%", "-1%", "1920x"}
	for _, s := range ok {
		p := validProfile()
		p.Size = s
		if err := ValidateProfile(p); err != nil {
			t.Errorf("size %q: %v", s, err)
		}
	}
	for _, s := range bad {
		p := validProfile()
		p.Size = s
		if err := ValidateProfile(p); err == nil {
			t.Errorf("size %q: want error", s)
		}
	}
}

func TestValidateScale(t *testing.T) {
	t.Parallel()
	for _, sc := range []int{100, 140, 180} {
		p := validProfile()
		p.Scale = sc
		if err := ValidateProfile(p); err != nil {
			t.Errorf("scale %d: %v", sc, err)
		}
	}
	p := validProfile()
	p.Scale = 120
	if err := ValidateProfile(p); err == nil {
		t.Fatal("scale 120")
	}
}

func validProfile() Profile {
	return Profile{
		Name:              "work",
		Host:              "host",
		User:              "user",
		Client:            DefaultClient,
		DynamicResolution: true,
		Scale:             100,
	}
}

// These fields are rendered to the terminal and passed to FreeRDP, so a
// newline, tab or ESC must not survive validation.
func TestValidateProfile_RejectsControlCharacters(t *testing.T) {
	base := Profile{Name: "n", Host: "h", User: "u", Client: "sdl-freerdp3", Scale: 100}
	for _, bad := range []string{"a\nb", "a\tb", "a\x1b[31mb", "a\rb", "a\x00b"} {
		for _, tc := range []struct {
			field string
			mut   func(Profile) Profile
		}{
			{"name", func(p Profile) Profile { p.Name = bad; return p }},
			{"host", func(p Profile) Profile { p.Host = bad; return p }},
			{"user", func(p Profile) Profile { p.User = bad; return p }},
			{"domain", func(p Profile) Profile { p.Domain = bad; return p }},
			{"client", func(p Profile) Profile { p.Client = bad; return p }},
		} {
			if err := ValidateProfile(tc.mut(base)); err == nil {
				t.Errorf("%s = %q accepted", tc.field, bad)
			}
		}
	}
}

// Atoi accepts "+100", which would let "+100%" through as a scale of 100.
func TestValidateSize_RejectsASignedNumber(t *testing.T) {
	for _, bad := range []string{"+100%", "-100%", "+1920x1080", "1920x+1080"} {
		if err := validateSize(bad); err == nil {
			t.Errorf("size %q accepted", bad)
		}
	}
	for _, good := range []string{"100%", "1920x1080", ""} {
		if err := validateSize(good); err != nil {
			t.Errorf("size %q rejected: %v", good, err)
		}
	}
}

// A host with a space in it is no host FreeRDP can reach, and "bad host"
// used to be saved all the same. A config that already has one still opens,
// so one bad profile cannot lock the user out of the rest.
func TestHost_SpacesRejectedOnSaveNotOnLoad(t *testing.T) {
	for _, bad := range []string{"bad host", "a b:3389", "[::1] :3389", "host\u00a0name"} {
		p := Profile{Name: "work", Host: bad, User: "u", Client: DefaultClient, Scale: DefaultScale}
		if err := ValidateProfileInUse(p); err == nil {
			t.Errorf("host %q accepted for save", bad)
		}
		// "[::1] :3389" is refused on load as well, by the IPv6 rule that
		// predates this one; the rest are well formed apart from the space.
		if err := ValidateProfile(p); err != nil && bad != "[::1] :3389" {
			t.Errorf("host %q rejected on load: %v", bad, err)
		}
	}
	path := writeTOML(t, `
[[profiles]]
name = "work"
host = "bad host"
user = "u"
`)
	cfg, err := Open(path)
	if err != nil {
		t.Fatalf("config with a spaced host did not open: %v", err)
	}
	p, _ := cfg.Profile("work")
	p.Host = "other host"
	if err := cfg.Upsert(p, "work"); err == nil {
		t.Fatal("spaced host saved")
	}
}
