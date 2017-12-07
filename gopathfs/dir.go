package gopathfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hanwen/go-fuse/fuse"
)

// OpenDir overwrites the parent's OpenDir method.
func (gpf *GoPathFs) OpenDir(name string, context *fuse.Context) ([]fuse.DirEntry, fuse.Status) {
	if name == "" {
		return gpf.openTopDir()
	}

	if name == gpf.cfg.GoPkgPrefix {
		return gpf.openFirstPartyDir()
	}

	if strings.HasPrefix(name, gpf.cfg.GoPkgPrefix+"/") {
		return gpf.openFirstPartyChildDir(name)
	}

	entries := []fuse.DirEntry{}
	var status fuse.Status

	// Search in fall-through directories.
	for _, dir := range gpf.cfg.FallThrough {
		if dir == name || strings.HasPrefix(name, dir) {
			fname := filepath.Join(gpf.dirs.Workspace, name)
			entries, status = gpf.openUnderlyingDir(fname, nil /* excludes */, entries)
			if status == fuse.OK {
				return entries, fuse.OK
			}
			fmt.Printf("failed to open entry %s\n", fname)
		}
	}

	// Search in vendor directories.
	for _, vendor := range gpf.cfg.Vendors {
		entries, status = gpf.openVendorChildDir(vendor, name, entries)
		if status == fuse.OK {
			return entries, fuse.OK
		}
	}

	return nil, fuse.ENOENT
}

// Mkdir overwrites the parent's Mkdir method.
func (gpf *GoPathFs) Mkdir(name string, mode uint32, context *fuse.Context) fuse.Status {
	prefix := gpf.cfg.GoPkgPrefix + "/"
	if strings.HasPrefix(name, prefix) {
		return gpf.mkFirstPartyChildDir(name[len(prefix):], mode, context)
	}

	return gpf.mkThirdPartyChildDir(name, mode, context)
}

// Rmdir overwrites the parent's Rmdir method.
func (gpf *GoPathFs) Rmdir(name string, context *fuse.Context) fuse.Status {
	prefix := gpf.cfg.GoPkgPrefix + "/"
	if strings.HasPrefix(name, prefix) {
		return gpf.rmFirstPartyChildDir(name[len(prefix):], context)
	}

	return gpf.rmThirdPartyChildDir(name, context)
}

func (gpf *GoPathFs) openTopDir() ([]fuse.DirEntry, fuse.Status) {
	entries := []fuse.DirEntry{
		{
			Name: gpf.cfg.GoPkgPrefix,
			Mode: fuse.S_IFDIR,
		},
	}

	// Vendor directories.
	for _, vendor := range gpf.cfg.Vendors {
		entries, _ = gpf.openUnderlyingDir(filepath.Join(gpf.dirs.Workspace, vendor), gpf.cfg.FallThroughSet /* excludes */, entries)
	}

	// Fall-through directories.
	for _, dir := range gpf.cfg.FallThrough {
		dir = filepath.Join(gpf.dirs.Workspace, dir)
		fi, err := os.Stat(dir)
		if err != nil {
			fmt.Printf("Failed to access %s, %v", dir, err)
			continue
		}

		entry := fuse.DirEntry{
			Name: fi.Name(),
			Mode: fuse.S_IFREG,
		}
		if fi.IsDir() {
			entry.Mode = fuse.S_IFDIR
		}
		entries = append(entries, entry)
	}

	return entries, fuse.OK
}

func (gpf *GoPathFs) openFirstPartyDir() ([]fuse.DirEntry, fuse.Status) {
	h, err := os.Open(gpf.dirs.Workspace)
	if err != nil {
		return nil, fuse.ENOENT
	}
	defer h.Close()

	fis, err := h.Readdir(-1)
	if err != nil {
		return nil, fuse.ENOENT
	}

	entries := []fuse.DirEntry{}
	for _, fi := range fis {
		if gpf.isIgnored(fi.Name()) {
			continue
		}

		if gpf.isVendorDir(fi.Name()) {
			continue
		}

		if fi.IsDir() {
			entry := fuse.DirEntry{
				Name: fi.Name(),
				Mode: fuse.S_IFREG,
			}
			entry.Mode = fuse.S_IFDIR
			entries = append(entries, entry)
		}
	}

	return entries, fuse.OK
}

func (gpf *GoPathFs) openFirstPartyChildDir(name string) ([]fuse.DirEntry, fuse.Status) {
	name = name[len(gpf.cfg.GoPkgPrefix+"/"):]
	entries := []fuse.DirEntry{}

	entries, _ = gpf.openUnderlyingDir(filepath.Join(gpf.dirs.Workspace, name), gpf.cfg.FallThroughSet /* excludes */, entries)
	// Also search in bazel-genfiles.
	entries, _ = gpf.openUnderlyingDir(filepath.Join(gpf.dirs.Workspace, "bazel-genfiles", name), gpf.cfg.FallThroughSet /* excludes */, entries)

	return entries, fuse.OK
}

func (gpf *GoPathFs) openVendorChildDir(vendor, name string, entries []fuse.DirEntry) ([]fuse.DirEntry, fuse.Status) {
	entries, _ = gpf.openUnderlyingDir(filepath.Join(gpf.dirs.Workspace, vendor, name), gpf.cfg.FallThroughSet /* excludes */, entries)
	// Also search in bazel-genfiles.
	entries, _ = gpf.openUnderlyingDir(filepath.Join(gpf.dirs.Workspace, "bazel-genfiles", vendor, name), gpf.cfg.FallThroughSet /* excludes */, entries)

	return entries, fuse.OK
}

func (gpf *GoPathFs) openUnderlyingDir(dir string, excludes map[string]struct{}, entries []fuse.DirEntry) ([]fuse.DirEntry, fuse.Status) {
	h, err := os.Open(dir)
	if err != nil {
		return entries, fuse.ENOENT
	}
	defer h.Close()

	fis, err := h.Readdir(-1)
	if err != nil {
		return entries, fuse.ENOENT
	}

outterLoop:
	for _, fi := range fis {
		if fi.IsDir() {
			for _, e := range entries {
				if fi.Name() == e.Name {
					// The generated folder has the same name as the original
					// one.
					continue outterLoop
				}
				if _, ok := excludes[fi.Name()]; ok {
					// The folder should be excluded, e.g., when it has the same
					// name as a fall-through folder.
					continue outterLoop
				}
			}
		}

		entry := fuse.DirEntry{
			Name: fi.Name(),
			Mode: fuse.S_IFREG,
		}
		if fi.IsDir() {
			entry.Mode = fuse.S_IFDIR
		}
		entries = append(entries, entry)
	}

	return entries, fuse.OK
}

func (gpf *GoPathFs) mkFirstPartyChildDir(name string, mode uint32, context *fuse.Context) fuse.Status {
	name = filepath.Join(gpf.dirs.Workspace, name)
	if err := os.MkdirAll(name, os.FileMode(mode)); err != nil {
		return fuse.ENOENT
	}
	return fuse.OK
}

func (gpf *GoPathFs) mkThirdPartyChildDir(name string, mode uint32, context *fuse.Context) fuse.Status {
	if len(gpf.cfg.Vendors) == 0 {
		return fuse.ENOENT
	}

	name = filepath.Join(gpf.dirs.Workspace, gpf.cfg.Vendors[0], name)
	if err := os.MkdirAll(name, os.FileMode(mode)); err != nil {
		return fuse.ENOENT
	}
	return fuse.OK
}

func (gpf *GoPathFs) rmFirstPartyChildDir(name string, context *fuse.Context) fuse.Status {
	name = filepath.Join(gpf.dirs.Workspace, name)
	if err := os.RemoveAll(name); err != nil {
		return fuse.ENOENT
	}
	return fuse.OK
}

func (gpf *GoPathFs) rmThirdPartyChildDir(name string, context *fuse.Context) fuse.Status {
	if len(gpf.cfg.Vendors) == 0 {
		return fuse.ENOENT
	}

	name = filepath.Join(gpf.dirs.Workspace, gpf.cfg.Vendors[0], name)
	if err := os.RemoveAll(name); err != nil {
		return fuse.ENOENT
	}
	return fuse.OK
}
