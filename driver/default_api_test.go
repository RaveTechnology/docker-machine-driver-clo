package driver

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/rancher/machine/libmachine/state"
)

type apiRoundTrip func(*http.Request) (*http.Response, error)

func (f apiRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDefaultAPIFactorySurvivesThrottledRead(t *testing.T) {
	d, _ := fixture(t)
	d.clientFactory = nil
	d.Ownership = Ownership{Attempt: "unit-attempt", ProviderName: "unit-server", ProjectID: "project", ServerID: "vm", BootVolumeIDs: []string{"boot"}}
	if err := d.saveJournal(); err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	calls := 0
	http.DefaultTransport = apiRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/servers/vm/detail" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		calls++
		code, body := 200, `{"result":{"id":"vm","name":"unit-server","project":"project","status":"ACTIVE","switch_status":"ON"}}`
		if calls == 1 {
			code, body = 429, `{"message":"limited"}`
		}
		return &http.Response{StatusCode: code, Header: http.Header{
			"Content-Type": {"application/json"}, "Retry-After": {"0"},
		}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	got, err := d.GetState()
	if err != nil || got != state.Running || calls != 2 {
		t.Fatalf("default client lost a temporary read rejection: state=%v calls=%d err=%v", got, calls, err)
	}
}
