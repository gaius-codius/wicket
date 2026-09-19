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
