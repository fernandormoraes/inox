package fs_ns

//modification of https://github.com/go-git/go-billy/blob/master/memfs/storage.go (Apache 2.0 license)

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/util"
	core "github.com/inoxlang/inox/internal/core"
)

const separator = filepath.Separator

type MemFilesystem struct {
	s         *inMemStorage
	tempCount int
}

func NewMemFilesystem(maxTotalStorageSize core.ByteCount) *MemFilesystem {
	return &MemFilesystem{s: newInMemoryStorage(maxTotalStorageSize)}
}

func (fs MemFilesystem) Chroot(path string) (billy.Filesystem, error) {
	return nil, core.ErrNotImplemented
}

func (fs MemFilesystem) Root() string {
	panic(core.ErrNotImplemented)
}

func (fs MemFilesystem) Absolute(path string) (string, error) {
	if core.PathFrom(path).IsAbsolute() {
		return path, nil
	}
	return "", core.ErrNotImplemented
}

func (fs *MemFilesystem) Create(filename string) (billy.File, error) {
	return fs.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
}

func (fs *MemFilesystem) Open(filename string) (billy.File, error) {
	return fs.OpenFile(filename, os.O_RDONLY, 0)
}

func (fs *MemFilesystem) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	f, has := fs.s.Get(filename)
	if !has {
		if !isCreate(flag) {
			return nil, os.ErrNotExist
		}

		var err error
		f, err = fs.s.New(filename, perm, flag)
		if err != nil {
			return nil, err
		}
	} else {
		if isExclusive(flag) {
			return nil, os.ErrExist
		}

		if target, isLink := fs.resolveLink(filename, f); isLink {
			return fs.OpenFile(target, flag, perm)
		}
	}

	if f.mode.IsDir() {
		return nil, fmt.Errorf("cannot open directory: %s", filename)
	}

	return f.Duplicate(filename, perm, flag), nil
}

func (fs *MemFilesystem) resolveLink(fullpath string, f *inMemfile) (target string, isLink bool) {
	if !isSymlink(f.mode) {
		return fullpath, false
	}

	target = string(f.content.bytes)
	if !core.PathFrom(fullpath).IsAbsolute() {
		target = fs.Join(filepath.Dir(fullpath), target)
	}

	return target, true
}

func (fs *MemFilesystem) Stat(filename string) (os.FileInfo, error) {
	f, has := fs.s.Get(filename)
	if !has {
		return nil, os.ErrNotExist
	}

	fi, _ := f.Stat()

	var err error
	if target, isLink := fs.resolveLink(filename, f); isLink {
		fi, err = fs.Stat(target)
		if err != nil {
			return nil, err
		}
	}

	// the name of the file should always the name of the stated file, so we
	// overwrite the Stat returned from the storage with it, since the
	// filename may belong to a link.

	coreFileInfo := fi.(*core.FileInfo)
	coreFileInfo.BaseName_ = filepath.Base(filename)
	return coreFileInfo, nil
}

func (fs *MemFilesystem) Lstat(filename string) (os.FileInfo, error) {
	f, has := fs.s.Get(filename)
	if !has {
		return nil, os.ErrNotExist
	}

	return f.Stat()
}

func (fs *MemFilesystem) ReadDir(path string) ([]os.FileInfo, error) {
	//TODO: return error if not a dir

	if f, has := fs.s.Get(path); has {
		if target, isLink := fs.resolveLink(path, f); isLink {
			return fs.ReadDir(target)
		}
	}

	var entries []os.FileInfo
	for _, f := range fs.s.Children(path) {
		fi, _ := f.Stat()
		entries = append(entries, fi)
	}

	sort.Sort(SortableFileInfo(entries))

	return entries, nil
}

func (fs *MemFilesystem) MkdirAll(path string, perm os.FileMode) error {
	_, err := fs.s.New(path, perm|os.ModeDir, 0)
	return err
}

func (fs *MemFilesystem) TempFile(dir, prefix string) (billy.File, error) {
	return util.TempFile(fs, dir, prefix)
}

func (fs *MemFilesystem) getTempFilename(dir, prefix string) string {
	fs.tempCount++
	filename := fmt.Sprintf("%s_%d_%d", prefix, fs.tempCount, time.Now().UnixNano())
	return fs.Join(dir, filename)
}

func (fs *MemFilesystem) Rename(from, to string) error {
	return fs.s.Rename(from, to)
}

func (fs *MemFilesystem) Remove(filename string) error {
	return fs.s.Remove(filename)
}

func (fs *MemFilesystem) Join(elem ...string) string {
	return filepath.Join(elem...)
}

func (fs *MemFilesystem) Symlink(target, link string) error {
	_, err := fs.Stat(link)
	if err == nil {
		return os.ErrExist
	}

	if !os.IsNotExist(err) {
		return err
	}

	return util.WriteFile(fs, link, []byte(target), 0777|os.ModeSymlink)
}

func (fs *MemFilesystem) Readlink(link string) (string, error) {
	f, has := fs.s.Get(link)
	if !has {
		return "", os.ErrNotExist
	}

	if !isSymlink(f.mode) {
		return "", &os.PathError{
			Op:   "readlink",
			Path: link,
			Err:  fmt.Errorf("not a symlink"),
		}
	}

	return string(f.content.bytes), nil
}

// Capabilities implements the Capable interface.
func (fs *MemFilesystem) Capabilities() billy.Capability {
	return billy.WriteCapability |
		billy.ReadCapability |
		billy.ReadAndWriteCapability |
		billy.SeekCapability |
		billy.TruncateCapability
}

type inMemfile struct {
	name     string
	content  *inMemFileContent
	position int64
	flag     int
	mode     os.FileMode

	isClosed bool
}

func (f *inMemfile) Name() string {
	return f.name
}

func (f *inMemfile) Read(b []byte) (int, error) {
	n, err := f.ReadAt(b, f.position)
	f.position += int64(n)

	if err == io.EOF && n != 0 {
		err = nil
	}

	return n, err
}

func (f *inMemfile) ReadAt(b []byte, off int64) (int, error) {
	if f.isClosed {
		return 0, os.ErrClosed
	}

	if !isReadAndWrite(f.flag) && !isReadOnly(f.flag) {
		return 0, errors.New("read not supported")
	}

	n, err := f.content.ReadAt(b, off)

	return n, err
}

func (f *inMemfile) Seek(offset int64, whence int) (int64, error) {
	if f.isClosed {
		return 0, os.ErrClosed
	}

	switch whence {
	case io.SeekCurrent:
		f.position += offset
	case io.SeekStart:
		f.position = offset
	case io.SeekEnd:
		f.position = int64(f.content.Len()) + offset
	}

	return f.position, nil
}

func (f *inMemfile) Write(p []byte) (int, error) {
	if f.isClosed {
		return 0, os.ErrClosed
	}

	if !isReadAndWrite(f.flag) && !isWriteOnly(f.flag) {
		return 0, errors.New("write not supported")
	}

	n, err := f.content.WriteAt(p, f.position)
	f.position += int64(n)

	return n, err
}

func (f *inMemfile) Close() error {
	if f.isClosed {
		return os.ErrClosed
	}

	f.isClosed = true
	return nil
}

func (f *inMemfile) Truncate(size int64) error {
	return f.content.Truncate(size)
}

func (f *inMemfile) Duplicate(filename string, mode os.FileMode, flag int) billy.File {
	new := &inMemfile{
		name:    filename,
		content: f.content,
		mode:    mode,
		flag:    flag,
	}

	if isTruncate(flag) {
		new.content.Truncate(0)
	}

	if isAppend(flag) {
		new.position = int64(new.content.Len())
	}

	return new
}

func (f *inMemfile) Stat() (os.FileInfo, error) {
	return core.FileInfo{
		BaseName_:       f.name,
		Mode_:           core.FileMode(f.mode),
		Size_:           core.ByteCount(f.content.Len()),
		ModTime_:        core.Date(f.content.ModifTime()),
		HasCreationTime: true,
		CreationTime_:   core.Date(f.content.creationTime),
	}, nil
}

// Lock is a no-op in memfs.
func (f *inMemfile) Lock() error {
	return nil
}

// Unlock is a no-op in memfs.
func (f *inMemfile) Unlock() error {
	return nil
}

func (c *inMemFileContent) Truncate(size int64) error {
	if size <= int64(len(c.bytes)) {
		c.filesystemStorageSize.Add(-int64(len(c.bytes)))
		c.bytes = c.bytes[:size]
	} else {
		more := int(size) - len(c.bytes)

		if c.filesystemStorageSize.Add(int64(more)) > c.filesystemMaxStorageSize {
			return ErrInMemoryStorageLimitExceededDuringWrite
		}

		c.filesystemStorageSize.Add(-int64(len(c.bytes)))

		c.bytes = append(c.bytes, make([]byte, more)...)
	}

	return nil
}

func (c *inMemFileContent) Len() int {
	return len(c.bytes)
}
