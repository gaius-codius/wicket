package exporter

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteFile writes data to path, readable by the user only, since an
// exported file names the host and the account. Without force an existing
// file is kept, and the error is fs.ErrExist. The check and the create are
// one step, so a file that appears between them is kept too, and a link
// already at path is not followed. With force the file is replaced whole,
// through a temporary file and a rename, so a failed write leaves the old one
// as it was, and a link at path is replaced, not written through.
func WriteFile(path string, data []byte, force bool) error {
	if force {
		return replaceFile(path, data)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := writeAndClose(f, data); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func replaceFile(path string, data []byte) error {
	if info, err := os.Lstat(path); err == nil && info.IsDir() {
		return &fs.PathError{Op: "write", Path: path, Err: errors.New("is a directory")}
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".wicket-*.rdp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := writeAndClose(f, data); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// writeAndClose writes data and syncs it before closing, so a crash does not
// leave a file in place but empty.
func writeAndClose(f *os.File, data []byte) error {
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", f.Name(), err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync %s: %w", f.Name(), err)
	}
	return f.Close()
}
