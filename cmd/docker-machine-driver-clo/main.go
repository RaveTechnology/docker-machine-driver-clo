package main

import (
	"github.com/RaveTechnology/docker-machine-driver-clo/driver"
	"github.com/rancher/machine/libmachine/drivers/plugin"
)

func main() { plugin.RegisterDriver(driver.New("", "")) }
