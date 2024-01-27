package core

import (
	"runtime"
	"testing"

	"github.com/inoxlang/inox/internal/utils"
	"github.com/stretchr/testify/assert"
)

func TestMap(t *testing.T) {

	{
		runtime.GC()
		startMemStats := new(runtime.MemStats)
		runtime.ReadMemStats(startMemStats)

		defer utils.AssertNoMemoryLeak(t, startMemStats, 10, utils.AssertNoMemoryLeakOptions{
			CheckGoroutines: true,
			GoroutineCount:  runtime.NumGoroutine(),
		})
	}

	t.Run("property name mapper", func(t *testing.T) {
		ctx := NewContext(ContextConfig{})
		defer ctx.CancelGracefully()

		NewGlobalState(ctx)

		// should work with any Iprops
		mapper := PropertyName("name")

		obj := NewObjectFromMap(ValMap{"name": String("a")}, ctx)
		result := MapIterable(ctx, NewWrappedValueList(obj), mapper)
		assert.Equal(t, NewWrappedValueList(String("a")), result)

		fileInfo := FileInfo{BaseName_: "file.txt"}
		result = MapIterable(ctx, NewWrappedValueList(fileInfo), mapper)
		assert.Equal(t, NewWrappedValueList(String("file.txt")), result)
	})

	t.Run("key list mapper", func(t *testing.T) {
		ctx := NewContext(ContextConfig{})
		defer ctx.CancelGracefully()

		NewGlobalState(ctx)

		// should work with any Iprops
		mapper := KeyList{"name"}

		obj := NewObjectFromMap(ValMap{"name": String("a")}, ctx)
		result := MapIterable(ctx, NewWrappedValueList(obj), mapper)
		assert.Equal(t, NewWrappedValueList(objFrom(ValMap{
			"name": String("a"),
		})), result)

		fileInfo := FileInfo{BaseName_: "file.txt"}
		result = MapIterable(ctx, NewWrappedValueList(fileInfo), mapper)
		assert.Equal(t, NewWrappedValueList(objFrom(ValMap{
			"name": String("file.txt"),
		})), result)
	})

}
