package driver

import (
	"context"
	"github.com/RaveTechnology/clo-cloud-go/cloud"
	"testing"
)

func TestCreateRequiresPublicAddressWithPrivateSSH(t *testing.T) {
	d, f := fixture(t)
	d.PublicIPID = "reserved"
	f.addresses["reserved"] = cloud.Address{ID: "reserved", External: true, Status: "DOWN", IP: "188.120.250.144"}
	if err := d.PreCreateCheck(); err != nil {
		t.Fatal(err)
	}
	d.TimeoutSeconds = 1
	if err := d.Create(); err == nil {
		t.Fatal("Create accepted a missing reserved public interface with private SSH")
	}
}

type pendingBindingCloud struct {
	*fakeCloud
	reads      int
	readyAfter int
}

func (f *pendingBindingCloud) GetAddress(ctx context.Context, id string) (cloud.Address, error) {
	a, err := f.fakeCloud.GetAddress(ctx, id)
	if id == "reserved" && f.creates > 0 {
		f.reads++
		if f.readyAfter == 0 || f.reads < f.readyAfter {
			a.Status = "PROCESSING"
			a.AttachedServerID = ""
		}
	}
	return a, err
}
func TestCreateRequiresCompletedPublicBindingInBothSSHModes(t *testing.T) {
	for _, mode := range []string{"private", "public"} {
		t.Run(mode, func(t *testing.T) {
			d, f := fixture(t)
			d.PublicIPID = "reserved"
			d.SSHAddress = mode
			f.addresses["reserved"] = cloud.Address{ID: "reserved", External: true, Status: "DOWN", IP: "188.120.250.144"}
			f.server.AddressIDs = append(f.server.AddressIDs, "reserved")
			binding := &pendingBindingCloud{fakeCloud: f}
			d.clientFactory = func(string) (cloud.API, error) { return binding, nil }
			if err := d.PreCreateCheck(); err != nil {
				t.Fatal(err)
			}
			d.TimeoutSeconds = 1
			if err := d.Create(); err == nil {
				t.Fatal("Create accepted a processing public address without binding")
			}
		})
	}
}

func TestCreateWaitsUntilPublicBindingBecomesActive(t *testing.T) {
	d, f := fixture(t)
	d.PublicIPID = "reserved"
	f.addresses["reserved"] = cloud.Address{ID: "reserved", External: true, Status: "DOWN", IP: "188.120.250.144"}
	f.server.AddressIDs = append(f.server.AddressIDs, "reserved")
	binding := &pendingBindingCloud{fakeCloud: f, readyAfter: 3}
	d.clientFactory = func(string) (cloud.API, error) { return binding, nil }
	if err := d.Create(); err != nil {
		t.Fatal(err)
	}
	if binding.reads < 3 {
		t.Fatal("Create did not wait for asynchronous public binding")
	}
}
