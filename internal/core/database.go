package core

import (
	"errors"
	"fmt"
	"sync"

	"github.com/inoxlang/inox/internal/utils"
)

var (
	openDbFnRegistry     = map[Scheme]OpenDBFn{}
	openDbFnRegistryLock sync.Mutex

	ErrNonUniqueDbOpenFnRegistration                = errors.New("non unique open DB function registration")
	ErrNameCollisionWithInitialDatabasePropertyName = errors.New("name collision with initial database property name")

	DATABASE_PROPNAMES = []string{"update_schema", "close", "schema"}

	_ Value = (*DatabaseIL)(nil)
)

type DatabaseIL struct {
	inner            Database
	initialSchema    *ObjectPattern
	propertyNames    []string
	topLevelEntities map[string]Value

	NoReprMixin
	NotClonableMixin
}

type DbOpenConfiguration struct {
	Resource       SchemeHolder
	ResolutionData Value
	FullAccess     bool
}

type OpenDBFn func(ctx *Context, config DbOpenConfiguration) (Database, error)

type Database interface {
	Resource() SchemeHolder
	Schema() *ObjectPattern
	UpdateSchema(ctx *Context, schema *ObjectPattern) error
	TopLevelEntities() map[string]Value
	Close(ctx *Context) error
}

func WrapDatabase(inner Database) *DatabaseIL {
	schema := inner.Schema()

	propertyNames := utils.CopySlice(DATABASE_PROPNAMES)
	schema.ForEachEntry(func(propName string, propPattern Pattern, isOptional bool) error {
		if utils.SliceContains(DATABASE_PROPNAMES, propName) {
			panic(fmt.Errorf("%w: %s", ErrNameCollisionWithInitialDatabasePropertyName, propName))
		}
		propertyNames = append(propertyNames, propName)
		return nil
	})

	return &DatabaseIL{
		inner:            inner,
		initialSchema:    schema,
		propertyNames:    propertyNames,
		topLevelEntities: inner.TopLevelEntities(),
	}
}

func RegisterOpenDbFn(scheme Scheme, fn OpenDBFn) {
	openDbFnRegistryLock.Lock()
	defer openDbFnRegistryLock.Unlock()

	_, ok := openDbFnRegistry[scheme]
	if ok {
		panic(ErrNonUniqueDbOpenFnRegistration)
	}

	openDbFnRegistry[scheme] = fn
}

func GetOpenDbFn(scheme Scheme) (OpenDBFn, bool) {
	openDbFnRegistryLock.Lock()
	defer openDbFnRegistryLock.Unlock()

	fn, ok := openDbFnRegistry[scheme]

	return fn, ok
}

func (db *DatabaseIL) Resource() SchemeHolder {
	return db.inner.Resource()
}

func (db *DatabaseIL) UpdateSchema(ctx *Context, schema *ObjectPattern) error {
	return db.inner.UpdateSchema(ctx, schema)
}

func (db *DatabaseIL) Close(ctx *Context) error {
	return db.inner.Close(ctx)
}

func (db *DatabaseIL) GetGoMethod(name string) (*GoFunction, bool) {
	switch name {
	case "update_schema":
		return WrapGoMethod(db.UpdateSchema), true
	case "close":
		return WrapGoMethod(db.Close), true
	}
	return nil, false
}

func (db *DatabaseIL) Prop(ctx *Context, name string) Value {
	switch name {
	case "schema":
		return db.initialSchema
	}

	val, ok := db.topLevelEntities[name]
	if ok {
		return val
	}

	method, ok := db.GetGoMethod(name)
	if !ok {
		panic(FormatErrPropertyDoesNotExist(name, db))
	}
	return method
}

func (*DatabaseIL) SetProp(ctx *Context, name string, value Value) error {
	return ErrCannotSetProp
}

func (db *DatabaseIL) PropertyNames(ctx *Context) []string {
	return db.propertyNames
}
