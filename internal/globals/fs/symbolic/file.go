package internal

import (
	symbolic "github.com/inox-project/inox/internal/core/symbolic"
)

type File struct {
	symbolic.UnassignablePropsMixin
	_ int
}

func (r *File) Test(v SymbolicValue) bool {
	_, ok := v.(*File)
	return ok
}

func (r File) Clone(clones map[uintptr]SymbolicValue) SymbolicValue {
	return &File{}
}

func (f *File) GetGoMethod(name string) (*symbolic.GoFunction, bool) {
	switch name {
	case "read":
		return symbolic.WrapGoMethod(f.read), true
	case "write":
		return symbolic.WrapGoMethod(f.write), true
	case "close":
		return symbolic.WrapGoMethod(f.close), true
	case "info":
		return symbolic.WrapGoMethod(f.info), true
	}
	return &symbolic.GoFunction{}, false
}

func (f *File) Prop(name string) SymbolicValue {
	method, ok := f.GetGoMethod(name)
	if !ok {
		panic(symbolic.FormatErrPropertyDoesNotExist(name, f))
	}
	return method
}

func (*File) PropertyNames() []string {
	return []string{"read", "write", "close", "info"}
}

func (f *File) read(ctx *symbolic.Context) (*symbolic.ByteSlice, *symbolic.Error) {
	return &symbolic.ByteSlice{}, nil
}

func (f *File) write(ctx *symbolic.Context, data symbolic.Readable) *symbolic.Error {
	return nil
}

func (f *File) close(ctx *symbolic.Context) {
}

func (f *File) info(ctx *symbolic.Context) (*symbolic.FileInfo, *symbolic.Error) {
	return &symbolic.FileInfo{}, nil
}

func (r *File) Widen() (symbolic.SymbolicValue, bool) {
	return nil, false
}

func (a *File) IsWidenable() bool {
	return false
}

func (r *File) String() string {
	return "%file"
}

func (r *File) WidestOfType() SymbolicValue {
	return &File{}
}
