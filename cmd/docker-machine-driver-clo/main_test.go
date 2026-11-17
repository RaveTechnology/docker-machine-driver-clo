package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RaveTechnology/docker-machine-driver-clo/driver"
	"github.com/rancher/machine/libmachine/drivers"
	rpc "github.com/rancher/machine/libmachine/drivers/rpc"
)

func TestRealBinaryRPCSchemaAndSecretSerialization(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "docker-machine-driver-clo")
	if out, e := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); e != nil {
		t.Fatalf("build: %v %s", e, out)
	}
	factory := rpc.NewRPCClientDriverFactory()
	defer factory.Close()
	initial, _ := json.Marshal(driver.New("rpc-smoke", t.TempDir()))
	client, e := factory.NewRPCClientDriver(binary, initial)
	if e != nil {
		t.Fatal(e)
	}
	if client.DriverName() != "clo" || drivers.DriverUserdataFlag(client) != "clo-user-data-file" {
		t.Fatal("RPC schema incompatible")
	}
	options := &rpc.RPCFlags{Values: map[string]interface{}{
		"clo-api-token": "rpc-secret-must-not-persist", "clo-project-id": "project", "clo-image-id": "image",
		"clo-vcpus": 2, "clo-ram-gb": 4, "clo-boot-gb": 20, "clo-timeout-seconds": 900, "clo-ssh-address": "private",
	}}
	if e = client.SetConfigFromFlags(options); e != nil {
		t.Fatal(e)
	}
	raw, e := client.GetConfigRaw()
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(raw), "rpc-secret") || strings.Contains(strings.ToLower(string(raw)), "api-token") {
		t.Fatal("RPC config contains secret")
	}
	if e = client.SetConfigRaw(raw); e != nil {
		t.Fatal(e)
	}
	if client.GetSSHUsername() != "root" || client.GetMachineName() != "rpc-smoke" {
		t.Fatal("RPC reload lost config")
	}
}
