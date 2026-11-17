package driver

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/RaveTechnology/clo-cloud-go/cloud"
	"github.com/rancher/machine/libmachine/drivers"
	"github.com/rancher/machine/libmachine/mcnflag"
)

func (d *Driver) GetCreateFlags() []mcnflag.Flag {
	return []mcnflag.Flag{
		mcnflag.StringFlag{Name: "clo-api-token", EnvVar: "CLO_API_TOKEN", Usage: "CLO API token (cloud credential)"},
		mcnflag.StringFlag{Name: "clo-project-id", Usage: "Existing CLO project ID"},
		mcnflag.StringFlag{Name: "clo-image-id", Usage: "Ubuntu 24.04 image ID"},
		mcnflag.IntFlag{Name: "clo-vcpus", Value: 2, Usage: "Shared vCPU count"},
		mcnflag.IntFlag{Name: "clo-ram-gb", Value: 4, Usage: "RAM in GB"},
		mcnflag.IntFlag{Name: "clo-boot-gb", Value: 20, Usage: "Boot volume in GB"},
		mcnflag.StringFlag{Name: "clo-public-ip-id", Usage: "Optional reserved public IPv4 ID; pool must have quantity=1, delete-before-create"},
		mcnflag.StringFlag{Name: "clo-ssh-address", Value: "private", Usage: "SSH address: private or public; RKE2 always uses private node-ip"},
		mcnflag.StringFlag{Name: "clo-user-data-file", Usage: "Path to Rancher #cloud-config YAML"},
		mcnflag.IntFlag{Name: "clo-timeout-seconds", Value: 900, Usage: "Operation timeout in seconds"},
	}
}
func (d *Driver) SetConfigFromFlags(o drivers.DriverOptions) error {
	d.token = o.String("clo-api-token")
	d.ProjectID = o.String("clo-project-id")
	d.ImageID = o.String("clo-image-id")
	d.PublicIPID = o.String("clo-public-ip-id")
	d.SSHAddress = o.String("clo-ssh-address")
	d.UserDataFile = o.String("clo-user-data-file")
	d.VCPUs = o.Int("clo-vcpus")
	d.RAMGB = o.Int("clo-ram-gb")
	d.BootGB = o.Int("clo-boot-gb")
	d.TimeoutSeconds = o.Int("clo-timeout-seconds")
	return d.validateConfig()
}
func (d *Driver) validateConfig() error {
	if d.BaseDriver == nil || d.MachineName == "" || d.StorePath == "" {
		return errors.New("machine name and store path are required")
	}
	if d.ProjectID == "" || d.ImageID == "" {
		return errors.New("CLO project ID and image ID are required")
	}
	if d.VCPUs < 1 || d.VCPUs > 8 || d.RAMGB < 1 || d.RAMGB > 32 || d.BootGB < 10 {
		return errors.New("unsupported CLO shape: vCPU 1..8, RAM 1..32 GB, boot >=10 GB required")
	}
	if d.TimeoutSeconds < 1 || d.TimeoutSeconds > 3600 {
		return errors.New("operation timeout must be 1..3600 seconds")
	}
	if d.SSHAddress != "private" && d.SSHAddress != "public" {
		return errors.New("SSH address must be private or public")
	}
	if d.SSHAddress == "public" && d.PublicIPID == "" {
		return errors.New("public SSH requires an existing reserved public IPv4 ID")
	}
	if d.Ownership.ProjectID != "" && d.Ownership.ProjectID != d.ProjectID {
		return errors.New("configured project differs from machine ownership")
	}
	return nil
}
func (d *Driver) projectNetwork(ctx context.Context, a cloud.API) error {
	p, e := a.GetProject(ctx, d.ProjectID)
	if e != nil {
		return e
	}
	if p.ID != d.ProjectID {
		return errors.New("CLO project response does not match configured project")
	}
	networks, e := a.GetNetworks(ctx, d.ProjectID)
	if e != nil {
		return e
	}
	var cidrs []string
	for _, n := range networks {
		if n.Private {
			p, e := netip.ParsePrefix(n.CIDR)
			if e != nil || !p.Addr().Is4() || p != p.Masked() {
				return errors.New("invalid project private IPv4 network")
			}
			cidrs = append(cidrs, n.CIDR)
		}
	}
	if len(cidrs) != 1 {
		return fmt.Errorf("expected one private project IPv4 network, found %d", len(cidrs))
	}
	if d.PrivateCIDR != "" && d.PrivateCIDR != cidrs[0] {
		return errors.New("project network changed since machine creation")
	}
	d.PrivateCIDR = cidrs[0]
	return nil
}
func (d *Driver) availableIP(ctx context.Context, a cloud.API) error {
	if d.PublicIPID == "" {
		return nil
	}
	return cloud.Wait(ctx, d.interval(), func(ctx context.Context) (string, error) {
		ip, e := a.GetAddress(ctx, d.PublicIPID)
		if e != nil {
			return "", e
		}
		parsed, e := netip.ParseAddr(ip.IP)
		if ip.ID != d.PublicIPID || !ip.External || e != nil || !parsed.Is4() {
			return "", errors.New("reserved address must be an external IPv4")
		}
		if ip.AttachedServerID != "" {
			return "", errors.New("reserved IPv4 is already attached; delete the old machine before creating its replacement")
		}
		return strings.ToUpper(ip.Status), nil
	}, func(s string) bool { return s == "DOWN" || s == "FREE" })
}
