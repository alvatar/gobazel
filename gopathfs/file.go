package gopathfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hanwen/go-fuse/fuse"
	"github.com/hanwen/go-fuse/fuse/nodefs"
	"golang.org/x/sys/unix"
)

// Open overwrites the parent's Open method.
func (gpf *GoPathFs) Open(name string, flags uint32, context *fuse.Context) (file nodefs.File, code fuse.Status) {
	if gpf.debug {
		fmt.Printf("\nReqeusted to open file %s.\n", name)
	}

	if strings.HasPrefix(name, gpf.cfg.GoPkgPrefix+pathSeparator) {
		return gpf.openFirstPartyChildFile(name, flags, context)
	}

	// Search in fall-through directories.
	for _, path := range gpf.cfg.FallThrough {
		if path == name || strings.HasPrefix(name, path) {
			f, status := gpf.openUnderlyingFile(filepath.Join(gpf.dirs.Workspace, name), flags, context)
			if status == fuse.OK {
				return f, status
			}
			return nil, fuse.ENOENT
		}
	}

	// Search in vendor directories.
	for _, vendor := range gpf.cfg.Vendors {
		f, status := gpf.openVendorChildFile(vendor, name, flags, context)
		if status == fuse.OK {
			return f, status
		}
	}

	return nil, fuse.ENOENT
}

// Create overwrites the parent's Create method.
func (gpf *GoPathFs) Create(name string, flags uint32, mode uint32,
	context *fuse.Context) (file nodefs.File, code fuse.Status) {

	if gpf.debug {
		fmt.Printf("\nReqeusted to create file %s.\n", name)
	}

	prefix := gpf.cfg.GoPkgPrefix + pathSeparator
	if strings.HasPrefix(name, prefix) {
		return gpf.createFirstPartyChildFile(name[len(prefix):], flags, mode, context)
	}

	return gpf.createThirdPartyChildFile(name, flags, mode, context)
}

// Unlink overwrites the parent's Unlink method.
func (gpf *GoPathFs) Unlink(name string, context *fuse.Context) (code fuse.Status) {
	if gpf.debug {
		fmt.Printf("\nReqeusted to unlink file %s.\n", name)
	}

	prefix := gpf.cfg.GoPkgPrefix + pathSeparator
	if strings.HasPrefix(name, prefix) {
		name = filepath.Join(gpf.dirs.Workspace, name[len(prefix):])
		return gpf.unlinkUnderlyingFile(name, context)
	}

	// Vendor directories.
	for _, vendor := range gpf.cfg.Vendors {
		name = filepath.Join(gpf.dirs.Workspace, vendor, name)
		if status := gpf.unlinkUnderlyingFile(name, context); status == fuse.OK {
			return status
		}
	}

	return fuse.ENOSYS
}

// Rename overwrites the parent's Rename method.
func (gpf *GoPathFs) Rename(oldName string, newName string, context *fuse.Context) (code fuse.Status) {
	if gpf.debug {
		fmt.Printf("\nReqeusted to rename from %s to %s.\n", oldName, newName)
	}

	if strings.HasPrefix(oldName, gpf.cfg.GoPkgPrefix+pathSeparator) {
		oldName = filepath.Join(gpf.dirs.Workspace, oldName[len(gpf.cfg.GoPkgPrefix):])
		newName = filepath.Join(gpf.dirs.Workspace, newName[len(gpf.cfg.GoPkgPrefix):])
	} else {
		// Vendor directories.
		for _, vendor := range gpf.cfg.Vendors {
			oldName = filepath.Join(vendor, oldName)
			if _, err := os.Stat(oldName); err == nil {
				newName = filepath.Join(vendor, newName)
				break
			}
		}
		if newName == "" || oldName == "" {
			return fuse.ENOSYS
		}
	}

	if gpf.debug {
		fmt.Printf("Actual rename from %s to %s ... ", oldName, newName)
	}
	if err := os.Rename(oldName, newName); err != nil {
		if gpf.debug {
			fmt.Println("failed to rename file %s,", oldName, err)
		}
		return fuse.ENOSYS
	}
	if gpf.debug {
		fmt.Println("Succeeded to rename file %s.\n", oldName)
	}
	return fuse.OK
}

func (gpf *GoPathFs) openFirstPartyChildFile(name string, flags uint32,
	context *fuse.Context) (file nodefs.File, code fuse.Status) {

	name = name[len(gpf.cfg.GoPkgPrefix+pathSeparator):]

	// Search in GOROOT (for debugger).
	if name == "GOROOT" || strings.HasPrefix(name, "GOROOT"+pathSeparator) {
		f, status := gpf.openUnderlyingFile(filepath.Join(gpf.dirs.GoSDKDir, name[len("GOROOT"):]), flags, context)
		if status == fuse.OK {
			return f, status
		}
		return nil, fuse.ENOENT
	}

	f, status := gpf.openUnderlyingFile(filepath.Join(gpf.dirs.Workspace, name), flags, context)
	if status == fuse.OK {
		return f, status
	}

	// Also search in bazel-genfiles.
	return gpf.openUnderlyingFile(filepath.Join(gpf.dirs.Workspace, "bazel-genfiles", name), flags, context)
}

func (gpf *GoPathFs) openVendorChildFile(vendor, name string, flags uint32,
	context *fuse.Context) (file nodefs.File, code fuse.Status) {

	f, status := gpf.openUnderlyingFile(filepath.Join(gpf.dirs.Workspace, vendor, name), flags, context)
	if status == fuse.OK {
		return f, status
	}

	// Also search in bazel-genfiles.
	return gpf.openUnderlyingFile(filepath.Join(gpf.dirs.Workspace, "bazel-genfiles", vendor, name), flags, context)
}

func (gpf *GoPathFs) openUnderlyingFile(name string, flags uint32,
	context *fuse.Context) (file nodefs.File, code fuse.Status) {

	if gpf.debug {
		fmt.Printf("Actually opening file %s.\n", name)
	}

	if _, err := os.Stat(name); err != nil {
		if os.IsNotExist(err) {
			return nil, fuse.ENOENT
		}
	}

	if flags&fuse.O_ANYWRITE != 0 && unix.Access(name, unix.W_OK) != nil {
		fmt.Printf("File not writable: %s.\n", name)
		return nil, fuse.EPERM
	}

	f, err := os.OpenFile(name, int(flags), 0)
	if err != nil {
		fmt.Printf("Failed to open file: %s, %+v.\n", name, err)
		return nil, fuse.ENOENT
	}

	if gpf.debug {
		fmt.Printf("Succeeded to open file: %s.\n", name)
	}
	return nodefs.NewLoopbackFile(f), fuse.OK
}

func (gpf *GoPathFs) createFirstPartyChildFile(name string, flags uint32, mode uint32,
	context *fuse.Context) (file nodefs.File, code fuse.Status) {

	name = filepath.Join(gpf.dirs.Workspace, name)

	if gpf.debug {
		fmt.Printf("Actually creating file %s.\n", name)
	}

	f, err := os.Create(name)
	if err != nil {
		if gpf.debug {
			fmt.Printf("Failed to create file %s.\n", name)
		}
		return nil, fuse.EIO
	}

	if err = os.Chmod(name, os.FileMode(mode)); err != nil {
		fmt.Printf("Fail to chmod. file: %s, mode: %s, err: %#v.", name, os.FileMode(mode).String(), err)
	}

	if gpf.debug {
		fmt.Printf("Succeeded to create file %s.\n", name)
	}
	return nodefs.NewLoopbackFile(f), fuse.OK
}

func (gpf *GoPathFs) createThirdPartyChildFile(name string, flags uint32, mode uint32,
	context *fuse.Context) (file nodefs.File, code fuse.Status) {
	if len(gpf.cfg.Vendors) == 0 {
		return nil, fuse.EIO
	}

	name = filepath.Join(gpf.dirs.Workspace, gpf.cfg.Vendors[0], name)
	if gpf.debug {
		fmt.Printf("Actually creating file %s.\n", name)
	}

	f, err := os.Create(name)
	if err != nil {
		if gpf.debug {
			fmt.Printf("Failed to create file %s.\n", name)
		}
		return nil, fuse.EIO
	}

	if err = os.Chmod(name, os.FileMode(mode)); err != nil {
		fmt.Printf("Fail to chmod. file: %s, mode: %s, err: %#v.", name, os.FileMode(mode).String(), err)
	}

	if gpf.debug {
		fmt.Printf("Succeeded to create file %s.\n", name)
	}
	return nodefs.NewLoopbackFile(f), fuse.OK
}

func (gpf *GoPathFs) unlinkUnderlyingFile(name string, context *fuse.Context) (code fuse.Status) {
	if gpf.debug {
		fmt.Printf("Actually unlinking file %s.\n", name)
	}

	if err := os.Remove(name); err != nil {
		if gpf.debug {
			fmt.Printf("Failed to unlink file %s.\n", name)
		}
		return fuse.EIO
	}

	if gpf.debug {
		fmt.Printf("Succeeded to unlink file %s.\n", name)
	}
	return fuse.OK
}
