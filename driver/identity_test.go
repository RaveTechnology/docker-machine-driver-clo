package driver

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func identityFromBootstrap(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, item := range cfg["write_files"].([]any) {
		f := item.(map[string]any)
		if f["path"] != "/etc/clo/machine-identity.json" {
			continue
		}
		if f["permissions"] != "0600" {
			t.Fatal("identity not private")
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(f["content"].(string)), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	t.Fatal("identity missing from bootstrap")
	return nil
}

func TestDriverUsesSavedAttemptForIdentity(t *testing.T) {
	d, f := fixture(t)
	if err := d.PreCreateCheck(); err != nil {
		t.Fatal(err)
	}
	data, err := d.userData()
	if err != nil {
		t.Fatal(err)
	}
	got := identityFromBootstrap(t, data)
	if len(got) != 4 || got["schemaVersion"] != float64(1) || got["projectID"] != "project" || got["providerName"] != d.Ownership.ProviderName || got["attempt"] != d.Ownership.Attempt || got["attempt"] == "" {
		t.Fatalf("identity %+v ownership %+v", got, d.Ownership)
	}
	if strings.Contains(string(data), "secret-token") || strings.Contains(string(data), "apiToken") {
		t.Fatal("token in bootstrap")
	}
	if err := d.Create(); err != nil {
		t.Fatal(err)
	}
	actual, err := base64.StdEncoding.DecodeString(f.last.UserDataBase64)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, identityFromBootstrap(t, actual)) {
		t.Fatal("provider received different or missing identity")
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var restored Driver
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if err := restored.loadJournal(); err != nil {
		t.Fatal(err)
	}
	next, err := restored.userData()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, identityFromBootstrap(t, next)) {
		t.Fatal("restored bootstrap generated a new identity")
	}
}
