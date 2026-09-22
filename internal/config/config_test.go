package config

import (
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("MPX_CONFIG_DIR", t.TempDir())
	if err := Save("work", map[string]string{KeyAppID: "id1", KeyAppKey: "key1"}, map[string]string{KeyEndpoint: "https://eu.api.mathpix.com"}); err != nil {
		t.Fatal(err)
	}
	if err := Save("default", map[string]string{KeyAppID: "id0"}, nil); err != nil {
		t.Fatal(err)
	}
	work, err := Load("work")
	if err != nil {
		t.Fatal(err)
	}
	if work.Cred(KeyAppID) != "id1" || work.Cred(KeyAppKey) != "key1" {
		t.Errorf("work credentials not round-tripped: %+v", work.Credentials)
	}
	if work.Setting(KeyEndpoint) != "https://eu.api.mathpix.com" {
		t.Errorf("work endpoint not round-tripped: %+v", work.Settings)
	}
	def, err := Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if def.Cred(KeyAppID) != "id0" {
		t.Errorf("saving one profile clobbered another: %+v", def.Credentials)
	}
}
