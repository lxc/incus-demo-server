package main

import (
	"github.com/lxc/incus/client"
	"github.com/lxc/incus/shared/api"
)

func incusForceDelete(d incus.InstanceServer, name string) error {
	req := api.InstanceStatePut{
		Action:  "stop",
		Timeout: -1,
		Force:   true,
	}

	op, err := d.UpdateInstanceState(name, req, "")
	if err == nil {
		op.Wait()
	}

	op, err = d.DeleteInstance(name)
	if err != nil {
		return err
	}

	return op.Wait()
}
