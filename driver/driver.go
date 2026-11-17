// Package driver implements the Rancher machine plugin for CLO.
package driver

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/RaveTechnology/clo-cloud-go/cloud"
	"github.com/rancher/machine/libmachine/drivers"
)

type Ownership struct {
	Attempt, ProviderName, ProjectID, ServerID string
	BootVolumeIDs                              []string
	Pending, Deleting, Removed                 bool
}

type Driver struct {
	*drivers.BaseDriver
	ProjectID, ImageID, PublicIPID, SSHAddress, UserDataFile string
	VCPUs, RAMGB, BootGB, TimeoutSeconds                     int
	PrivateCIDR                                              string
	Ownership                                                Ownership
	token                                                    string
	allowCreate                                              bool
	pollInterval                                             time.Duration
	clientFactory                                            func(string) (cloud.API, error)
}

func New(name, store string) *Driver {
	return &Driver{BaseDriver: &drivers.BaseDriver{MachineName: name, StorePath: store, SSHUser: "root", SSHPort: 22}, VCPUs: 2, RAMGB: 4, BootGB: 20, TimeoutSeconds: 900, SSHAddress: "private"}
}

var _ drivers.Driver = (*Driver)(nil)

func (d *Driver) DriverName() string { return "clo" }
func (d *Driver) interval() time.Duration {
	if d.pollInterval > 0 {
		return d.pollInterval
	}
	return 2 * time.Second
}
func (d *Driver) operation() (context.Context, context.CancelFunc) {
	n := d.TimeoutSeconds
	if n <= 0 {
		n = 900
	}
	return context.WithTimeout(context.Background(), time.Duration(n)*time.Second)
}
func (d *Driver) api() (cloud.API, error) {
	token := os.Getenv("CLO_API_TOKEN")
	if token == "" {
		token = d.token
	}
	if token == "" {
		return nil, errors.New("CLO_API_TOKEN is required")
	}
	if d.clientFactory != nil {
		return d.clientFactory(token)
	}
	return cloud.New(token)
}
