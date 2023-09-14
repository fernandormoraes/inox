package core

import (
	"strconv"
	"testing"
	"time"

	parse "github.com/inoxlang/inox/internal/parse"
	permkind "github.com/inoxlang/inox/internal/permkind"
	"github.com/stretchr/testify/assert"
)

func TestObject(t *testing.T) {

	t.Run("SetProp", func(t *testing.T) {
		ctx := NewContexWithEmptyState(ContextConfig{}, nil)

		{
			obj := NewObjectFromMap(ValMap{}, ctx)
			obj.SetProp(ctx, "a", Int(1))
			obj.SetProp(ctx, "b", Int(2))
			assert.Equal(t, []string{"a", "b"}, obj.keys)
			assert.Equal(t, []Serializable{Int(1), Int(2)}, obj.values)
		}

		{
			obj := NewObjectFromMap(ValMap{}, ctx)
			obj.SetProp(ctx, "b", NewObjectFromMap(ValMap{}, ctx))

			obj.OnMutation(ctx, func(ctx *Context, mutation Mutation) (registerAgain bool) {
				return true
			}, MutationWatchingConfiguration{Depth: IntermediateDepthWatching})

			if !assert.Len(t, obj.propMutationCallbacks, 1) {
				return
			}

			handleB := obj.propMutationCallbacks[0]

			obj.SetProp(ctx, "a", NewObjectFromMap(ValMap{}, ctx))

			if !assert.Len(t, obj.propMutationCallbacks, 2) {
				return
			}

			//the handle of B should have moved to the second position
			assert.Equal(t, handleB, obj.propMutationCallbacks[1])
		}

		t.Run("sould wait current transaction to be finished", func(t *testing.T) {
			ctx1 := NewContexWithEmptyState(ContextConfig{}, nil)
			tx1 := StartNewTransaction(ctx1)
			ctx2 := NewContexWithEmptyState(ContextConfig{}, nil)

			obj := NewObjectFromMap(ValMap{}, ctx1)

			signal := make(chan struct{}, 1)

			go func() {
				StartNewTransaction(ctx2)

				<-signal
				obj.SetProp(ctx2, "b", Int(2))
				signal <- struct{}{}
			}()

			obj.SetProp(ctx1, "a", Int(1))

			signal <- struct{}{}

			if !assert.Equal(t, Int(1), obj.Prop(ctx1, "a")) {
				<-signal
				return
			}

			//at this point tx1 is not finished so the 'b' property should not be set because .SetProp is waiting.

			time.Sleep(time.Millisecond)

			if !assert.NoError(t, tx1.Commit(ctx1)) {
				return
			}
			<-signal

			if !assert.Equal(t, Int(1), obj.Prop(ctx2, "a")) {
				return
			}

			if !assert.Equal(t, Int(2), obj.Prop(ctx2, "b")) {
				return
			}
		})
	})

	t.Run("lifetime jobs", func(t *testing.T) {
		// the operation duration depends on the time required to pause a job, that depends on the lthread's interpreter.
		MAX_OPERATION_DURATION := 500 * time.Microsecond

		// setup creates a new object with as many jobs as job codes
		setup := func(t *testing.T, jobCodes ...string) (*Context, *Object) {

			ctx := NewContext(ContextConfig{
				Permissions: []Permission{
					LThreadPermission{Kind_: permkind.Create},
					GlobalVarPermission{Kind_: permkind.Use, Name: "*"},
					GlobalVarPermission{Kind_: permkind.Read, Name: "*"},
				},
			})

			state := NewGlobalState(ctx)
			state.Module = &Module{
				MainChunk: &parse.ParsedChunk{
					Node: parse.MustParseChunk(""),
				},
			}

			valMap := ValMap{
				"a": Int(1),
			}

			for i, jobCode := range jobCodes {
				job := createTestLifetimeJob(t, state, jobCode)
				if job == nil {
					return nil, nil
				}
				valMap[strconv.Itoa(i)] = job
			}

			obj := NewObjectFromMap(valMap, ctx)
			assert.NoError(t, obj.instantiateLifetimeJobs(ctx))
			return ctx, obj
		}

		for i := 0; i < 5; i++ {

			t.Run("empty job should be done in a short time", func(t *testing.T) {
				ctx, obj := setup(t, "")
				defer ctx.Cancel()

				time.Sleep(10 * time.Millisecond)
				jobs := obj.jobInstances()
				if !assert.Len(t, jobs, 1) {
					return
				}
				assert.True(t, jobs[0].thread.IsDone())

				<-jobs[0].thread.wait_result
			})

			t.Run("two empty jobs should be done in a short time", func(t *testing.T) {
				ctx, obj := setup(t, "", "")
				defer ctx.Cancel()

				time.Sleep(10 * time.Millisecond)

				jobs := obj.jobInstances()
				if !assert.Len(t, jobs, 2) {
					return
				}
				assert.True(t, jobs[0].thread.IsDone())
				assert.True(t, jobs[1].thread.IsDone())
			})

			t.Run("job doing a simple operation should be done in a short time", func(t *testing.T) {
				ctx, obj := setup(t, "(1 + 1)")
				defer ctx.Cancel()

				time.Sleep(10 * time.Millisecond)
				jobs := obj.jobInstances()
				if !assert.Len(t, jobs, 1) {
					return
				}
				assert.True(t, jobs[0].thread.IsDone())
			})

			t.Run("accessing a prop should be fast", func(t *testing.T) {
				ctx, obj := setup(t, `
					c = 0
					for i in 1..1_000_000 {
						c += 1
					}
				`)
				defer ctx.Cancel()

				time.Sleep(10 * time.Millisecond)
				jobs := obj.jobInstances()
				if !assert.Len(t, jobs, 1) {
					return
				}
				assert.False(t, jobs[0].thread.IsDone())

				start := time.Now()
				obj.Prop(ctx, "a")
				assert.Less(t, time.Since(start), MAX_OPERATION_DURATION)
			})

			t.Run("setting a property should be fast", func(t *testing.T) {
				ctx, obj := setup(t, `
					c = 0
					for i in 1..1_000_000 {
						c += 1
					}
				`)
				defer ctx.Cancel()

				time.Sleep(10 * time.Millisecond)
				jobs := obj.jobInstances()
				if !assert.Len(t, jobs, 1) {
					return
				}
				assert.False(t, jobs[0].thread.IsDone())

				start := time.Now()
				obj.SetProp(ctx, "a", Int(2))
				assert.Less(t, time.Since(start), MAX_OPERATION_DURATION)
			})
		}

	})

}

func TestList(t *testing.T) {

	t.Run("append", func(t *testing.T) {
		ctx := NewContexWithEmptyState(ContextConfig{}, nil)

		list := NewWrappedValueList()
		list.append(ctx, Int(1))
		list.append(ctx, Int(2))

		assert.Equal(t, []Serializable{Int(1), Int(2)}, list.GetOrBuildElements(ctx))
	})
}

func TestUdata(t *testing.T) {

	t.Run("getEntryAtIndexes", func(t *testing.T) {
		udata := &UData{
			Root: Int(1),
			HiearchyEntries: []UDataHiearchyEntry{
				{
					Value: Int(2),
					Children: []UDataHiearchyEntry{
						{Value: Int(3)},
						{
							Value: Int(4),
							Children: []UDataHiearchyEntry{
								{
									Value: Int(5),
								},
							},
						},
					},
				},
				{Value: Int(6)},
			},
		}

		entry, ok := udata.getEntryAtIndexes(0)
		if !assert.True(t, ok) {
			return
		}
		assert.Equal(t, Int(2), entry.Value)

		entry, ok = udata.getEntryAtIndexes(0, 0)
		if !assert.True(t, ok) {
			return
		}
		assert.Equal(t, Int(3), entry.Value)

		entry, ok = udata.getEntryAtIndexes(0, 1)
		if !assert.True(t, ok) {
			return
		}
		assert.Equal(t, Int(4), entry.Value)

		entry, ok = udata.getEntryAtIndexes(0, 1, 0)
		if !assert.True(t, ok) {
			return
		}
		assert.Equal(t, Int(5), entry.Value)

		entry, ok = udata.getEntryAtIndexes(1)
		if !assert.True(t, ok) {
			return
		}
		assert.Equal(t, Int(6), entry.Value)
	})

}
