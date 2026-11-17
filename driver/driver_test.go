package driver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RaveTechnology/clo-cloud-go/cloud"
	"github.com/rancher/machine/libmachine/drivers"
	"github.com/rancher/machine/libmachine/state"
)

type fakeCloud struct {
	creates             int
	deleted             [][]string
	last                cloud.CreateRequest
	server              cloud.Server
	volumes             map[string]cloud.Volume
	addresses           map[string]cloud.Address
	networks            []cloud.Network
	listed              []cloud.Server
	createErr           error
	volumeErr           error
	projectErr          error
	absent              bool
	tokens              []string
	powers              []string
	privateAddressReads int
	privateAddressDelay int
}

func (f *fakeCloud) GetProject(context.Context, string) (cloud.Project, error) {
	if f.projectErr != nil {
		return cloud.Project{}, f.projectErr
	}
	return cloud.Project{ID: "project", Status: "ACTIVE"}, nil
}
func (f *fakeCloud) GetServer(context.Context, string) (cloud.Server, error) {
	if f.absent {
		return cloud.Server{}, &cloud.APIError{Status: 404}
	}
	return f.server, nil
}
func (f *fakeCloud) ListServers(context.Context, string) ([]cloud.Server, error) {
	return f.listed, nil
}
func (f *fakeCloud) CreateServer(_ context.Context, _ string, r cloud.CreateRequest) (string, error) {
	f.creates++
	f.last = r
	f.server.Name = r.Name
	if f.createErr != nil {
		return "", f.createErr
	}
	if r.PublicIPID != "" {
		address := f.addresses[r.PublicIPID]
		address.Status = "ACTIVE"
		address.AttachedServerID = f.server.ID
		f.addresses[r.PublicIPID] = address
	}
	return f.server.ID, nil
}
func (f *fakeCloud) DeleteServer(_ context.Context, _ string, boots []string) error {
	f.deleted = append(f.deleted, append([]string{}, boots...))
	f.absent = true
	delete(f.volumes, "boot")
	return nil
}
func (f *fakeCloud) GetVolume(_ context.Context, id string) (cloud.Volume, error) {
	if f.volumeErr != nil {
		return cloud.Volume{}, f.volumeErr
	}
	v, ok := f.volumes[id]
	if !ok {
		return v, &cloud.APIError{Status: 404}
	}
	return v, nil
}
func (f *fakeCloud) GetAddress(_ context.Context, id string) (cloud.Address, error) {
	v, ok := f.addresses[id]
	if !ok {
		return v, &cloud.APIError{Status: 404}
	}
	if id == "private" {
		f.privateAddressReads++
		if f.privateAddressReads <= f.privateAddressDelay {
			v.IP = ""
		}
	}
	return v, nil
}
func (f *fakeCloud) GetNetworks(context.Context, string) ([]cloud.Network, error) {
	return f.networks, nil
}
func (f *fakeCloud) Power(_ context.Context, _ string, op string) error {
	f.powers = append(f.powers, op)
	return nil
}
func fixture(t *testing.T) (*Driver, *fakeCloud) {
	t.Helper()
	t.Setenv("CLO_API_TOKEN", "test-secret")
	d := New("rke2-test", t.TempDir())
	d.ProjectID = "project"
	d.ImageID = "image"
	d.VCPUs = 2
	d.RAMGB = 4
	d.BootGB = 20
	d.SSHAddress = "private"
	d.TimeoutSeconds = 30
	d.pollInterval = time.Millisecond
	f := &fakeCloud{server: cloud.Server{ID: "vm", ProjectID: "project", Status: "ACTIVE", AddressIDs: []string{"private"}, DiskIDs: []string{"boot"}}, volumes: map[string]cloud.Volume{"boot": {ID: "boot", Bootable: true, AttachedServerID: "vm", Status: "IN_USE"}}, addresses: map[string]cloud.Address{"private": {ID: "private", IP: "10.174.80.3", AttachedServerID: "vm"}}, networks: []cloud.Network{{ID: "net", CIDR: "10.174.80.0/21", Private: true, Status: "ACTIVE"}}}
	useAPI(d, f)
	return d, f
}
func useAPI(d *Driver, f *fakeCloud) {
	d.clientFactory = func(token string) (cloud.API, error) { f.tokens = append(f.tokens, token); return f, nil }
}

func TestCreateUsesGeneratedKeyAndPrivateBootstrap(t *testing.T) {
	d, f := fixture(t)
	if e := d.Create(); e != nil {
		t.Fatal(e)
	}
	if f.creates != 1 || f.last.PublicIPID != "" {
		t.Fatalf("create %#v", f.last)
	}
	data, e := base64.StdEncoding.DecodeString(f.last.UserDataBase64)
	if e != nil {
		t.Fatal(e)
	}
	pub, e := os.ReadFile(d.GetSSHKeyPath() + ".pub")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(data), strings.TrimSpace(string(pub))) || !strings.Contains(string(data), "10.174.80.0/21") {
		t.Fatal("key/network missing")
	}
	if d.Ownership.ServerID != "vm" || len(d.Ownership.BootVolumeIDs) != 1 || d.Ownership.Pending {
		t.Fatalf("ownership %#v", d.Ownership)
	}
	ip, e := d.GetSSHHostname()
	if e != nil || ip != "10.174.80.3" {
		t.Fatalf("SSH %s %v", ip, e)
	}
	info, e := os.Stat(d.journalPath())
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("journal permissions %v %v", info, e)
	}
}

func TestCreateWaitsForPrivateAddress(t *testing.T) {
	d, f := fixture(t)
	f.privateAddressDelay = 2
	if e := d.Create(); e != nil {
		t.Fatal(e)
	}
	if f.privateAddressReads < 3 || f.creates != 1 {
		t.Fatal("did not wait for asynchronous address assignment")
	}
}

func TestRPCReloadUsesFreshCredentialAndRemovePreservesData(t *testing.T) {
	d, f := fixture(t)
	if e := d.Create(); e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(d)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(raw), "test-secret") {
		t.Fatal("credential leaked into config")
	}
	reloaded := New("", "")
	if e = json.Unmarshal(raw, reloaded); e != nil {
		t.Fatal(e)
	}
	useAPI(reloaded, f)
	credentialCalls := len(f.tokens)
	t.Setenv("CLO_API_TOKEN", "rotated-secret")
	s, e := reloaded.GetState()
	if e != nil || s != state.Running {
		t.Fatalf("state %v %v", s, e)
	}
	f.server.DiskIDs = append(f.server.DiskIDs, "data")
	f.volumes["data"] = cloud.Volume{ID: "data", AttachedServerID: "vm", Bootable: true}
	f.server.AddressIDs = append(f.server.AddressIDs, "public")
	f.addresses["public"] = cloud.Address{ID: "public", External: true, IP: "188.120.250.144", AttachedServerID: "vm"}
	if e = reloaded.Remove(); e != nil {
		t.Fatal(e)
	}
	if e = reloaded.Remove(); e != nil {
		t.Fatal(e)
	}
	if len(f.deleted) != 1 || len(f.deleted[0]) != 1 || f.deleted[0][0] != "boot" {
		t.Fatalf("unsafe delete %#v", f.deleted)
	}
	if _, ok := f.volumes["data"]; !ok {
		t.Fatal("data deleted")
	}
	if len(f.addresses) != 2 {
		t.Fatal("address deleted")
	}
	for _, token := range f.tokens[credentialCalls:] {
		if token != "rotated-secret" {
			t.Fatal("stale credential")
		}
	}
}

func TestAmbiguousCreateNeverRepeatsPOST(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		t.Run(string(rune('0'+count)), func(t *testing.T) {
			d, f := fixture(t)
			f.createErr = errors.New("transport timeout")
			if e := d.Create(); e == nil {
				t.Fatal("timeout hidden")
			}
			if f.creates != 1 || !d.Ownership.Pending {
				t.Fatal("attempt not persisted")
			}
			raw, _ := json.Marshal(d)
			reloaded := New("", "")
			if e := json.Unmarshal(raw, reloaded); e != nil {
				t.Fatal(e)
			}
			useAPI(reloaded, f)
			reloaded.pollInterval = time.Millisecond
			for i := 0; i < count; i++ {
				s := f.server
				s.ID = string(rune('a' + i))
				if count == 1 {
					s.ID = "vm"
				}
				f.listed = append(f.listed, s)
			}
			e := reloaded.Create()
			if count == 1 && e != nil {
				t.Fatal(e)
			}
			if count != 1 && e == nil {
				t.Fatal("unresolved attempt accepted")
			}
			if f.creates != 1 {
				t.Fatal("blind POST retry")
			}
		})
	}
}

func TestJournalRestoresAttemptWhenRPCStateWasNotSaved(t *testing.T) {
	d, f := fixture(t)
	f.createErr = errors.New("timeout")
	if e := d.Create(); e == nil {
		t.Fatal("wanted failure")
	}
	next := New(d.MachineName, d.StorePath)
	next.ProjectID = d.ProjectID
	next.ImageID = d.ImageID
	next.VCPUs = 2
	next.RAMGB = 4
	next.BootGB = 20
	next.SSHAddress = "private"
	next.TimeoutSeconds = 1
	useAPI(next, f)
	if e := next.Create(); e == nil {
		t.Fatal("pending attempt not restored")
	}
	if f.creates != 1 || next.Ownership.Attempt != d.Ownership.Attempt {
		t.Fatal("attempt lost")
	}
}

func TestPreCreateCheckMakesPendingAttemptDurableBeforeMachineSave(t *testing.T) {
	d, f := fixture(t)
	if e := d.PreCreateCheck(); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(d)
	if !d.Ownership.Pending || d.Ownership.Attempt == "" {
		t.Fatal("pre-create config has no pending attempt")
	}
	reloaded := New("", "")
	if e := json.Unmarshal(raw, reloaded); e != nil {
		t.Fatal(e)
	}
	useAPI(reloaded, f)
	// Rancher may restore only its pre-create Secret after the pod is lost.
	if e := os.Remove(d.journalPath()); e != nil {
		t.Fatal(e)
	}
	if e := reloaded.Create(); e == nil {
		t.Fatal("uncertain pre-create attempt retried")
	}
	if f.creates != 0 {
		t.Fatal("new RPC blindly created")
	}
	// Only the process which prepared the attempt can send its first POST.
	if e := d.Create(); e != nil {
		t.Fatal(e)
	}
	if f.creates != 1 {
		t.Fatal("initial process did not create")
	}
}

func TestConfigAndResourceAmbiguityFailBeforePOST(t *testing.T) {
	for _, kind := range []string{"project", "shape", "network", "public", "ssh", "userdata"} {
		t.Run(kind, func(t *testing.T) {
			d, f := fixture(t)
			switch kind {
			case "project":
				d.ProjectID = ""
			case "shape":
				d.VCPUs = 0
			case "network":
				f.networks = append(f.networks, f.networks[0])
			case "public":
				d.PublicIPID = "busy"
				f.addresses["busy"] = cloud.Address{ID: "busy", External: true, IP: "188.120.250.144", Status: "ACTIVE", AttachedServerID: "other"}
			case "ssh":
				d.SSHAddress = "public"
			case "userdata":
				d.UserDataFile = t.TempDir() + "/missing"
			}
			if e := d.Create(); e == nil {
				t.Fatal("invalid config accepted")
			}
			if f.creates != 0 {
				t.Fatal("unsafe create")
			}
		})
	}
}
func TestPublicReservedAddressAndAmbiguousPrivateAddress(t *testing.T) {
	d, f := fixture(t)
	d.PublicIPID = "reserved"
	d.SSHAddress = "public"
	f.addresses["reserved"] = cloud.Address{ID: "reserved", External: true, Status: "DOWN", IP: "188.120.250.144"}
	f.server.AddressIDs = append(f.server.AddressIDs, "reserved")
	if e := d.Create(); e != nil {
		t.Fatal(e)
	}
	if f.last.PublicIPID != "reserved" {
		t.Fatal("reserved IP not used")
	}
	ip, e := d.GetIP()
	if e != nil || ip != "188.120.250.144" {
		t.Fatalf("public SSH %s %v", ip, e)
	}
	f.server.AddressIDs = append(f.server.AddressIDs, "another-private")
	f.addresses["another-private"] = cloud.Address{ID: "another-private", IP: "10.174.80.4", AttachedServerID: "vm"}
	if _, e = d.GetIP(); e == nil {
		t.Fatal("ambiguous private IP hidden by public SSH choice")
	}
}

func TestPrivateCreateRefusesUnexpectedPublicAddress(t *testing.T) {
	d, f := fixture(t)
	f.server.AddressIDs = append(f.server.AddressIDs, "unexpected-public")
	f.addresses["unexpected-public"] = cloud.Address{ID: "unexpected-public", IP: "188.120.250.152", External: true, AttachedServerID: "vm"}
	if e := d.Create(); e == nil {
		t.Fatal("private-only VM accepted an unrequested public interface")
	}
	if d.Ownership.ServerID != "vm" {
		t.Fatal("failed VM cannot be cleaned up")
	}
}
func TestRemoveRefusesForeignOwnership(t *testing.T) {
	for _, kind := range []string{"project", "name", "boot", "config"} {
		t.Run(kind, func(t *testing.T) {
			d, f := fixture(t)
			if e := d.Create(); e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "project":
				f.server.ProjectID = "foreign"
			case "name":
				f.server.Name = "foreign"
			case "boot":
				v := f.volumes["boot"]
				v.AttachedServerID = "foreign"
				f.volumes["boot"] = v
			case "config":
				d.ProjectID = "foreign"
			}
			if e := d.Remove(); e == nil {
				t.Fatal("foreign ownership accepted")
			}
			if len(f.deleted) > 0 {
				t.Fatal("foreign resource deleted")
			}
		})
	}
}
func TestRemoveReportsOrphanBootInsteadOfClaimingCleanup(t *testing.T) {
	d, f := fixture(t)
	if e := d.Create(); e != nil {
		t.Fatal(e)
	}
	f.absent = true
	if e := d.Remove(); e == nil {
		t.Fatal("orphan boot hidden")
	}
	if len(f.deleted) != 0 {
		t.Fatal("deleted absent VM")
	}
}

func TestRemoveErrorCannotBeMistakenForSuccessfulMachineCLI404(t *testing.T) {
	d, f := fixture(t)
	if e := d.Create(); e != nil {
		t.Fatal(e)
	}
	f.volumeErr = &cloud.APIError{Status: 404, Message: "volume not found"}
	e := d.Remove()
	if e == nil {
		t.Fatal("missing boot ownership accepted")
	}
	// machine commands/removeRemoteMachine ignores every error containing this phrase.
	if strings.Contains(strings.ToLower(e.Error()), "not found") {
		t.Fatalf("machine CLI would discard state while VM is still present: %v", e)
	}
	if f.absent || len(f.deleted) > 0 {
		t.Fatal("VM was deleted without boot validation")
	}
}

func TestRemoveDoesNotTreatWrongCredentialScopeAsDeletedResources(t *testing.T) {
	d, f := fixture(t)
	if e := d.Create(); e != nil {
		t.Fatal(e)
	}
	f.projectErr = &cloud.APIError{Status: 404, Message: "project not found"}
	f.absent = true
	f.volumes = map[string]cloud.Volume{}
	if e := d.Remove(); e == nil {
		t.Fatal("wrong credential scope falsely confirmed cleanup")
	}
	if d.Ownership.Removed {
		t.Fatal("lost ownership with an inaccessible project")
	}
}

func TestRemoveResumesAfterSavingIntentBeforeDeleteRequest(t *testing.T) {
	d, f := fixture(t)
	if e := d.Create(); e != nil {
		t.Fatal(e)
	}
	d.Ownership.Deleting = true
	if e := d.saveJournal(); e != nil {
		t.Fatal(e)
	}
	if e := d.Remove(); e != nil {
		t.Fatal(e)
	}
	if len(f.deleted) != 1 {
		t.Fatal("delete intent could not resume")
	}
}
func TestDriverContract(t *testing.T) {
	d, f := fixture(t)
	var _ drivers.Driver = d
	if drivers.DriverUserdataFlag(d) != "clo-user-data-file" {
		t.Fatal("userdata flag incompatible")
	}
	if e := d.Create(); e != nil {
		t.Fatal(e)
	}
	for _, fn := range []func() error{d.Start, d.Stop, d.Restart} {
		if e := fn(); e != nil {
			t.Fatal(e)
		}
	}
	if strings.Join(f.powers, ",") != "start,stop,reboot" {
		t.Fatal(f.powers)
	}
	if e := d.Kill(); e == nil {
		t.Fatal("unsupported hard kill simulated")
	}
}
