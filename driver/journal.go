package driver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func (d *Driver) journalPath() string { return d.ResolveStorePath("clo-state.json") }
func (d *Driver) loadJournal() error {
	if d.BaseDriver == nil || d.MachineName == "" || d.StorePath == "" {
		return errors.New("machine store is not configured")
	}
	b, e := os.ReadFile(d.journalPath())
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return fmt.Errorf("read CLO ownership journal: %w", e)
	}
	var own Ownership
	if e = json.Unmarshal(b, &own); e != nil {
		return errors.New("invalid CLO ownership journal")
	}
	if own.Attempt == "" || own.ProjectID == "" || own.ProviderName == "" {
		return errors.New("incomplete CLO ownership journal")
	}
	if d.Ownership.Attempt != "" && d.Ownership.Attempt != own.Attempt {
		return errors.New("CLO journal and machine config describe different attempts")
	}
	d.Ownership = own
	return nil
}
func (d *Driver) saveJournal() error {
	path := d.journalPath()
	dir := filepath.Dir(path)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	b, e := json.Marshal(d.Ownership)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".clo-state-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if e = os.Rename(tmp, path); e != nil {
		return e
	}
	parent, e := os.Open(dir)
	if e != nil {
		return e
	}
	defer parent.Close()
	return parent.Sync()
}
