package driver

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/RaveTechnology/clo-cloud-go/bootstrap"
	"github.com/RaveTechnology/clo-cloud-go/cloud"
	"github.com/rancher/machine/libmachine/ssh"
	"github.com/rancher/machine/libmachine/state"
)

var errAddressPending = errors.New("VM address assignment is still pending")
var machineIgnoredPhrase = regexp.MustCompile(`(?i)not found`)

type removeFailure struct{ cause error }

func (e removeFailure) Error() string {
	return "CLO cleanup incomplete: " + machineIgnoredPhrase.ReplaceAllString(e.cause.Error(), "resource missing")
}
func (e removeFailure) Unwrap() error { return e.cause }

func (d *Driver) PreCreateCheck() error {
	if e := d.loadJournal(); e != nil {
		return e
	}
	if e := d.validateConfig(); e != nil {
		return e
	}
	ctx, cancel := d.operation()
	defer cancel()
	a, e := d.api()
	if e != nil {
		return e
	}
	if e = d.projectNetwork(ctx, a); e != nil {
		return e
	}
	if d.Ownership.Attempt != "" {
		return nil
	}
	if e = d.availableIP(ctx, a); e != nil {
		return e
	}
	if _, e = d.userData(); e != nil {
		return e
	}
	nonce := make([]byte, 8)
	if _, e = rand.Read(nonce); e != nil {
		return e
	}
	attempt := hex.EncodeToString(nonce)
	name := regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(strings.ToLower(d.MachineName), "-")
	if len(name) > 40 {
		name = name[:40]
	}
	d.Ownership = Ownership{Attempt: attempt, ProviderName: "clo-" + name + "-" + attempt, ProjectID: d.ProjectID, Pending: true}
	if e = d.saveJournal(); e != nil {
		return e
	}
	// Rancher saves GetConfigRaw and the machine directory after this method,
	// before Create. A restored process cannot know whether POST was issued.
	d.allowCreate = true
	return nil
}
func (d *Driver) userData() ([]byte, error) {
	keyPath := d.GetSSHKeyPath()
	if e := os.MkdirAll(filepath.Dir(keyPath), 0700); e != nil {
		return nil, e
	}
	_, e := os.Stat(keyPath)
	if os.IsNotExist(e) {
		if _, pubErr := os.Stat(keyPath + ".pub"); !os.IsNotExist(pubErr) {
			return nil, errors.New("SSH public key exists without its private key")
		}
		if e = ssh.GenerateSSHKey(keyPath); e != nil {
			return nil, errors.New("cannot generate machine SSH key")
		}
	} else if e != nil {
		return nil, e
	}
	pub, e := os.ReadFile(keyPath + ".pub")
	if e != nil {
		return nil, errors.New("cannot read machine SSH public key")
	}
	var input []byte
	if d.UserDataFile != "" {
		input, e = os.ReadFile(d.UserDataFile)
		if e != nil {
			return nil, errors.New("cannot read CLO user-data file")
		}
	}
	if d.Ownership.Attempt == "" {
		// PreCreateCheck validates the input and creates the SSH key before it
		// persists an attempt. The actual create payload is composed afterwards.
		return bootstrap.Compose(input, strings.TrimSpace(string(pub)), d.PrivateCIDR)
	}
	return bootstrap.ComposeWithIdentity(input, strings.TrimSpace(string(pub)), d.PrivateCIDR, bootstrap.MachineIdentity{
		SchemaVersion: 1, ProjectID: d.Ownership.ProjectID,
		ProviderName: d.Ownership.ProviderName, Attempt: d.Ownership.Attempt,
	})
}
func (d *Driver) Create() error {
	if e := d.loadJournal(); e != nil {
		return e
	}
	if e := d.validateConfig(); e != nil {
		return e
	}
	if d.Ownership.Attempt == "" {
		if e := d.PreCreateCheck(); e != nil {
			return e
		}
	}
	if d.Ownership.Removed {
		return errors.New("machine was removed; create a new machine configuration")
	}
	ctx, cancel := d.operation()
	defer cancel()
	a, e := d.api()
	if e != nil {
		return e
	}
	if e = d.projectNetwork(ctx, a); e != nil {
		return e
	}
	if d.Ownership.ServerID == "" {
		if !d.allowCreate {
			if e = d.reconcile(ctx, a); e != nil {
				return e
			}
		} else {
			d.allowCreate = false
			if e = d.availableIP(ctx, a); e != nil {
				return e
			}
			data, e := d.userData()
			if e != nil {
				return e
			}
			id, e := a.CreateServer(ctx, d.ProjectID, cloud.CreateRequest{Name: d.Ownership.ProviderName, ImageID: d.ImageID, VCPUs: d.VCPUs, RAMGB: d.RAMGB, BootGB: d.BootGB, PublicIPID: d.PublicIPID, UserDataBase64: base64.StdEncoding.EncodeToString(data)})
			if e != nil {
				return fmt.Errorf("create result is uncertain; reconcile this attempt before any retry: %w", e)
			}
			if id == "" {
				return errors.New("create returned no server ID; attempt remains pending")
			}
			d.Ownership.ServerID = id
			if e = d.saveJournal(); e != nil {
				return e
			}
		}
	}
	if e = d.captureBoot(ctx, a); e != nil {
		return e
	}
	if e = cloud.Wait(ctx, d.interval(), func(ctx context.Context) (string, error) { s, e := d.ownedServer(ctx, a); return s.Status, e }, func(s string) bool { return s == "ACTIVE" || s == "RUNNING" }); e != nil {
		return e
	}
	if e = cloud.Wait(ctx, d.interval(), func(ctx context.Context) (string, error) {
		_, err := d.ip(ctx, a)
		if errors.Is(err, errAddressPending) {
			return "PENDING", nil
		}
		if err != nil {
			return "", err
		}
		return "READY", nil
	}, func(s string) bool { return s == "READY" }); e != nil {
		return e
	}
	d.Ownership.Pending = false
	return d.saveJournal()
}
func (d *Driver) reconcile(ctx context.Context, a cloud.API) error {
	if d.Ownership.Attempt == "" {
		return errors.New("cannot reconcile without a saved attempt")
	}
	servers, e := a.ListServers(ctx, d.Ownership.ProjectID)
	if e != nil {
		return e
	}
	var matches []cloud.Server
	for _, s := range servers {
		if s.Name == d.Ownership.ProviderName && s.ProjectID == d.Ownership.ProjectID {
			matches = append(matches, s)
		}
	}
	if len(matches) != 1 {
		return fmt.Errorf("pending CLO attempt has %d matching VMs; refusing a new create or guessed cleanup", len(matches))
	}
	d.Ownership.ServerID = matches[0].ID
	return d.saveJournal()
}
func (d *Driver) ownedServer(ctx context.Context, a cloud.API) (cloud.Server, error) {
	s, e := a.GetServer(ctx, d.Ownership.ServerID)
	if e != nil {
		return s, e
	}
	if s.ID != d.Ownership.ServerID || s.ProjectID != d.Ownership.ProjectID || s.Name != d.Ownership.ProviderName {
		return s, errors.New("VM identity does not match saved CLO ownership")
	}
	return s, nil
}
func (d *Driver) captureBoot(ctx context.Context, a cloud.API) error {
	if len(d.Ownership.BootVolumeIDs) > 0 {
		return nil
	}
	var boots []string
	e := cloud.Wait(ctx, d.interval(), func(ctx context.Context) (string, error) {
		s, e := d.ownedServer(ctx, a)
		if e != nil {
			return "", e
		}
		boots = nil
		for _, id := range s.DiskIDs {
			v, e := a.GetVolume(ctx, id)
			if e != nil {
				return "", e
			}
			if v.Bootable {
				if v.AttachedServerID != s.ID {
					return "", errors.New("initial boot volume is attached to another server")
				}
				boots = append(boots, id)
			}
		}
		if len(boots) > 1 {
			return "", errors.New("ambiguous initial boot volume ownership")
		}
		if len(boots) == 1 {
			return "READY", nil
		}
		return s.Status, nil
	}, func(s string) bool { return s == "READY" })
	if e != nil {
		return e
	}
	d.Ownership.BootVolumeIDs = boots
	return d.saveJournal()
}
func (d *Driver) ip(ctx context.Context, a cloud.API) (string, error) {
	s, e := d.ownedServer(ctx, a)
	if e != nil {
		return "", e
	}
	if d.PrivateCIDR == "" {
		if e = d.projectNetwork(ctx, a); e != nil {
			return "", e
		}
	}
	prefix, e := netip.ParsePrefix(d.PrivateCIDR)
	if e != nil {
		return "", errors.New("missing valid project CIDR")
	}
	private := map[string]bool{}
	public := ""
	for _, id := range s.AddressIDs {
		ip, e := a.GetAddress(ctx, id)
		if e != nil {
			return "", e
		}
		if ip.ID != id {
			return "", errors.New("VM address response has a different ID")
		}
		if ip.External && id != d.PublicIPID {
			return "", errors.New("VM has an unrequested public address; refusing private-only or reserved-IP acceptance")
		}
		address, e := netip.ParseAddr(ip.IP)
		if e != nil {
			continue
		}
		if ip.AttachedServerID != "" && ip.AttachedServerID != s.ID {
			return "", errors.New("VM address belongs to another server")
		}
		if !ip.External && prefix.Contains(address) {
			private[address.String()] = true
		}
		if ip.External && id == d.PublicIPID {
			if !address.Is4() {
				return "", errors.New("reserved public address is not IPv4")
			}
			switch strings.ToUpper(ip.Status) {
			case "ERROR", "UNKNOWN", "BLOCKED":
				return "", fmt.Errorf("reserved public IPv4 entered terminal state %s", ip.Status)
			case "ACTIVE":
				if ip.AttachedServerID == s.ID {
					public = address.String()
				}
			}
		}
	}
	if len(private) == 0 {
		return "", errAddressPending
	}
	if len(private) != 1 {
		return "", fmt.Errorf("expected one private VM IPv4, found %d", len(private))
	}
	if d.PublicIPID != "" && public == "" {
		return "", errAddressPending
	}
	for address := range private {
		d.IPAddress = address
	}
	if d.SSHAddress == "public" {
		if public == "" {
			return "", errAddressPending
		}
		d.IPAddress = public
	}
	return d.IPAddress, nil
}
func (d *Driver) GetIP() (string, error) {
	if e := d.loadJournal(); e != nil {
		return "", e
	}
	if e := d.validateConfig(); e != nil {
		return "", e
	}
	a, e := d.api()
	if e != nil {
		return "", e
	}
	ctx, cancel := d.operation()
	defer cancel()
	return d.ip(ctx, a)
}
func (d *Driver) GetSSHHostname() (string, error) { return d.GetIP() }
func (d *Driver) GetURL() (string, error) {
	ip, e := d.GetIP()
	if e != nil {
		return "", e
	}
	return "tcp://" + ip + ":2376", nil
}
func (d *Driver) GetState() (state.State, error) {
	if e := d.loadJournal(); e != nil {
		return state.Error, e
	}
	if e := d.validateConfig(); e != nil {
		return state.Error, e
	}
	a, e := d.api()
	if e != nil {
		return state.Error, e
	}
	ctx, cancel := d.operation()
	defer cancel()
	if d.Ownership.Removed || d.Ownership.ServerID == "" {
		return state.NotFound, nil
	}
	s, e := d.ownedServer(ctx, a)
	if cloud.IsNotFound(e) {
		return state.NotFound, nil
	}
	if e != nil {
		return state.Error, e
	}
	switch strings.ToUpper(s.Status) {
	case "ACTIVE", "RUNNING":
		return state.Running, nil
	case "STOPPED", "SHUTOFF":
		return state.Stopped, nil
	case "STOPPING":
		return state.Stopping, nil
	case "ERROR", "BLOCKED", "UNKNOWN":
		return state.Error, nil
	default:
		return state.Starting, nil
	}
}
func (d *Driver) Remove() (err error) {
	// machine commands/removeRemoteMachine treats *any* error containing "not found"
	// as success, including a missing volume while the VM is still present.
	defer func() {
		if err != nil {
			err = removeFailure{cause: err}
		}
	}()
	if e := d.loadJournal(); e != nil {
		return e
	}
	if e := d.validateConfig(); e != nil {
		return e
	}
	a, e := d.api()
	if e != nil {
		return e
	}
	ctx, cancel := d.operation()
	defer cancel()
	if d.Ownership.Attempt == "" {
		return nil
	}
	if d.Ownership.Removed {
		return nil
	}
	// A token for another account can make every resource UUID appear absent.
	// Confirm access to the owning project before interpreting resource 404s.
	project, e := a.GetProject(ctx, d.Ownership.ProjectID)
	if e != nil {
		return e
	}
	if project.ID != d.Ownership.ProjectID {
		return errors.New("project identity does not match saved CLO ownership")
	}
	if d.Ownership.ServerID == "" {
		if e = d.reconcile(ctx, a); e != nil {
			return e
		}
	}
	server, e := d.ownedServer(ctx, a)
	if !cloud.IsNotFound(e) {
		if e != nil {
			return e
		}
		if !d.Ownership.Deleting || !strings.Contains(strings.ToUpper(server.Status), "DELET") {
			if e = d.captureBoot(ctx, a); e != nil {
				return e
			}
			for _, id := range d.Ownership.BootVolumeIDs {
				v, e := a.GetVolume(ctx, id)
				if e != nil {
					return e
				}
				if !v.Bootable || v.AttachedServerID != d.Ownership.ServerID {
					return errors.New("saved boot volume no longer belongs to this VM")
				}
			}
			d.Ownership.Deleting = true
			if e = d.saveJournal(); e != nil {
				return e
			}
			if e = a.DeleteServer(ctx, d.Ownership.ServerID, d.Ownership.BootVolumeIDs); e != nil && !cloud.IsNotFound(e) {
				d.Ownership.Deleting = false
				if journalErr := d.saveJournal(); journalErr != nil {
					return journalErr
				}
				return e
			}
		}
		if e = cloud.Wait(ctx, d.interval(), func(ctx context.Context) (string, error) {
			_, e := d.ownedServer(ctx, a)
			if cloud.IsNotFound(e) {
				return "GONE", nil
			}
			return "DELETING", e
		}, func(s string) bool { return s == "GONE" }); e != nil {
			return e
		}
	}
	if len(d.Ownership.BootVolumeIDs) == 0 {
		return errors.New("VM absent but boot ownership is unknown; cleanup is unconfirmed")
	}
	for _, id := range d.Ownership.BootVolumeIDs {
		_, e := a.GetVolume(ctx, id)
		if !cloud.IsNotFound(e) {
			if e != nil {
				return e
			}
			return errors.New("VM absent but an owned boot volume remains; cleanup is unconfirmed")
		}
	}
	d.Ownership.Removed = true
	d.Ownership.Pending = false
	d.Ownership.Deleting = false
	return d.saveJournal()
}
func (d *Driver) power(op string) error {
	if e := d.loadJournal(); e != nil {
		return e
	}
	if e := d.validateConfig(); e != nil {
		return e
	}
	a, e := d.api()
	if e != nil {
		return e
	}
	ctx, cancel := d.operation()
	defer cancel()
	if _, e = d.ownedServer(ctx, a); e != nil {
		return e
	}
	return a.Power(ctx, d.Ownership.ServerID, op)
}
func (d *Driver) Start() error   { return d.power("start") }
func (d *Driver) Stop() error    { return d.power("stop") }
func (d *Driver) Restart() error { return d.power("reboot") }
func (d *Driver) Kill() error {
	return errors.New("CLO API does not expose a supported hard-kill operation")
}
